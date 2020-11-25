/*
Copyright © 2019 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	backoff "github.com/cenkalti/backoff/v3"
	"github.com/dsnet/compress/bzip2"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
)

var resultcollectorCmd = &cobra.Command{
	Use:   "resultscollector",
	Short: "A tool to do an OpenSCAP scan from a pod.",
	Long:  "A tool to do an OpenSCAP scan from a pod.",
	Run:   resultCollectorMain,
}

func init() {
	rootCmd.AddCommand(resultcollectorCmd)
	defineResultcollectorFlags(resultcollectorCmd)
}

type scapresultsConfig struct {
	ArfFile            string
	XccdfFile          string
	ExitCodeFile       string
	CmdOutputFile      string
	WarningsOutputFile string
	ScanName           string
	ConfigMapName      string
	NodeName           string
	Namespace          string
	ResultServerURI    string
	Timeout            int64
	Cert               string
	Key                string
	CA                 string
}

func defineResultcollectorFlags(cmd *cobra.Command) {
	cmd.Flags().String("arf-file", "", "The ARF file to watch.")
	cmd.Flags().String("results-file", "", "The XCCDF results file to watch.")
	cmd.Flags().String("exit-code-file", "", "A file containing the oscap command's exit code.")
	cmd.Flags().String("oscap-output-file", "", "A file containing the oscap command's output.")
	cmd.Flags().String("warnings-output-file", "", "A file containing the warnings to output.")
	cmd.Flags().String("owner", "", "The compliance scan that owns the configMap objects.")
	cmd.Flags().String("config-map-name", "", "The configMap to upload to, typically the podname.")
	cmd.Flags().String("node-name", "", "The node that was scanned.")
	cmd.Flags().String("namespace", "openshift-compliance", "Running pod namespace.")
	cmd.Flags().Int64("timeout", 3600, "How long to wait for the file.")
	cmd.Flags().String("resultserveruri", "", "The resultserver URI name.")
	cmd.Flags().String("tls-client-cert", "", "The path to the client and CA PEM cert bundle.")
	cmd.Flags().String("tls-client-key", "", "The path to the client PEM key.")
	cmd.Flags().String("tls-ca", "", "The path to the CA certificate.")

	flags := cmd.Flags()
	flags.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	flags.AddGoFlagSet(flag.CommandLine)
}

func parseConfig(cmd *cobra.Command) *scapresultsConfig {
	var conf scapresultsConfig
	conf.ArfFile = getValidStringArg(cmd, "arf-file")
	conf.XccdfFile = getValidStringArg(cmd, "results-file")
	conf.ExitCodeFile = getValidStringArg(cmd, "exit-code-file")
	conf.CmdOutputFile = getValidStringArg(cmd, "oscap-output-file")
	conf.ScanName = getValidStringArg(cmd, "owner")
	conf.ConfigMapName = getValidStringArg(cmd, "config-map-name")
	conf.Namespace = getValidStringArg(cmd, "namespace")
	conf.Cert = getValidStringArg(cmd, "tls-client-cert")
	conf.Key = getValidStringArg(cmd, "tls-client-key")
	conf.CA = getValidStringArg(cmd, "tls-ca")
	conf.Timeout, _ = cmd.Flags().GetInt64("timeout")
	conf.ResultServerURI, _ = cmd.Flags().GetString("resultserveruri")
	// Set default if needed
	if conf.ResultServerURI == "" {
		conf.ResultServerURI = "http://" + conf.ScanName + "-rs:8080/"
	}
	conf.WarningsOutputFile, _ = cmd.Flags().GetString("warnings-output-file")

	// platform scans have no node name
	conf.NodeName, _ = cmd.Flags().GetString("node-name")

	logf.SetLogger(zap.Logger())

	return &conf
}

func getOpenSCAPScanInstance(name, namespace string, client *complianceCrClient) (*compv1alpha1.ComplianceScan, error) {
	key := types.NamespacedName{Name: name, Namespace: namespace}
	scan := &compv1alpha1.ComplianceScan{}
	err := client.client.Get(context.TODO(), key, scan)
	if err != nil {
		log.Error(err, "Error getting scan instance", "ComplianceScan.Name", scan.Name, "ComplianceScan.Namespace", scan.Namespace)
		return nil, err
	}

	return scan, nil
}

func waitForResultsFile(filename string, timeout int64) *os.File {
	readFileTimeoutChan := make(chan *os.File, 1)
	// G304 (CWE-22) is addressed by this.
	cleanFileName := filepath.Clean(filename)

	go func() {
		for {
			// Note that we're cleaning the filename path above.
			// #nosec
			file, err := os.Open(cleanFileName)
			if err == nil {
				fileinfo, err := file.Stat()
				// Only try to use the file if it already has contents.
				// This way we avoid race conditions between the side-car and
				// this script.
				if err == nil && fileinfo.Size() > 0 {
					readFileTimeoutChan <- file
				}
			} else if !os.IsNotExist(err) {
				log.Error(err, "Couldn't open results file")
				os.Exit(1)
			}
			time.Sleep(1 * time.Second)
		}
	}()

	select {
	case file := <-readFileTimeoutChan:
		log.Info("Results file found, will upload it.", "resuts-file", filename)
		return file
	case <-time.After(time.Duration(timeout) * time.Second):
		log.Error(fmt.Errorf("Timed out waiting for results file"), "Timeout. Aborting.")
		os.Exit(1)
	}

	// We shouldn't get here.
	return nil
}

func resultNeedsCompression(contents []byte) bool {
	return len(contents) > 1048570
}

func compressResults(contents []byte) ([]byte, error) {
	// Encode the contents ascii, compress it with gzip, b64encode it so it
	// can be stored in the configmap.
	var buffer bytes.Buffer
	w, err := bzip2.NewWriter(&buffer, &bzip2.WriterConfig{Level: bzip2.BestCompression})
	if err != nil {
		return nil, err
	}
	w.Write([]byte(contents))
	w.Close()
	return buffer.Bytes(), nil
}

type resultFileContents struct {
	contents   []byte
	compressed bool
}

func readResultsFile(filename string, timeout int64) (*resultFileContents, error) {
	var err error
	var rfContents resultFileContents

	handle := waitForResultsFile(filename, timeout)
	// #nosec
	defer handle.Close()

	rfContents.contents, err = ioutil.ReadAll(handle)
	if err != nil {
		return nil, err
	}

	if resultNeedsCompression(rfContents.contents) {
		rfContents.contents, err = compressResults(rfContents.contents)
		log.Info("File needs compression", "results-file", filename)
		if err != nil {
			log.Error(err, "Error: Compression failed")
			return nil, err
		}
		rfContents.compressed = true
		log.Info("Compressed results bytes size", "bytes", len(rfContents.contents))
	}

	return &rfContents, nil
}

func readWarningsFile(filename string) string {
	// No warnings file provided, no need to parse anything
	if filename == "" {
		return ""
	}
	contents, err := ioutil.ReadFile(filepath.Clean(filename))
	if os.IsNotExist(err) {
		// warnings file provided, but no warnings were generated
		return ""
	}
	if err != nil {
		DBG("Error while reading warnings file: %v", err)
		return ""
	}

	return strings.Trim(string(contents), "\n")
}

func uploadToResultServer(arfContents *resultFileContents, scapresultsconf *scapresultsConfig) error {
	return backoff.Retry(func() error {
		url := scapresultsconf.ResultServerURI
		log.Info("Trying to upload to resultserver", "url", url)
		reader := bytes.NewReader(arfContents.contents)
		transport, err := getMutualHttpsTransport(scapresultsconf)
		if err != nil {
			log.Error(err, "Failed to get https transport")
			return err
		}
		client := &http.Client{Transport: transport}
		req, _ := http.NewRequest("POST", url, reader)
		req.Header.Add("Content-Type", "application/xml")
		req.Header.Add("X-Report-Name", scapresultsconf.ConfigMapName)
		if arfContents.compressed {
			req.Header.Add("Content-Encoding", "bzip2")
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Error(err, "Failed to upload results to server")
			return err
		}
		defer resp.Body.Close()
		bytesresp, err := httputil.DumpResponse(resp, true)
		if err != nil {
			log.Error(err, "Failed to parse response")
			return err
		}
		log.Info(string(bytesresp))
		return nil
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
}

func uploadResultConfigMap(xccdfContents *resultFileContents, exitcode string,
	scapresultsconf *scapresultsConfig, client *complianceCrClient) error {
	warnings := readWarningsFile(scapresultsconf.WarningsOutputFile)

	return backoff.Retry(func() error {
		log.Info("Trying to upload results ConfigMap")
		openscapScan, err := getOpenSCAPScanInstance(scapresultsconf.ScanName, scapresultsconf.Namespace, client)
		if err != nil {
			return err
		}
		confMap := utils.GetResultConfigMap(openscapScan, scapresultsconf.ConfigMapName, "results",
			scapresultsconf.NodeName, xccdfContents.contents, xccdfContents.compressed, exitcode, warnings)
		err = client.client.Create(context.TODO(), confMap)

		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
}

func uploadErrorConfigMap(errorMsg *resultFileContents, exitcode string,
	scapresultsconf *scapresultsConfig, client *complianceCrClient) error {
	warnings := readWarningsFile(scapresultsconf.WarningsOutputFile)

	return backoff.Retry(func() error {
		log.Info("Trying to upload error ConfigMap")
		openscapScan, err := getOpenSCAPScanInstance(scapresultsconf.ScanName, scapresultsconf.Namespace, client)
		if err != nil {
			return err
		}
		confMap := utils.GetResultConfigMap(openscapScan, scapresultsconf.ConfigMapName, "error-msg",
			scapresultsconf.NodeName, errorMsg.contents, errorMsg.compressed, exitcode, warnings)
		err = client.client.Create(context.TODO(), confMap)

		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
}

func handleCompleteSCAPResults(exitcode string, scapresultsconf *scapresultsConfig, client *complianceCrClient) {
	arfContents, err := readResultsFile(scapresultsconf.ArfFile, scapresultsconf.Timeout)
	if err != nil {
		log.Error(err, "Failed to read ARF file")
		os.Exit(1)
	}

	xccdfContents, err := readResultsFile(scapresultsconf.XccdfFile, scapresultsconf.Timeout)
	if err != nil {
		log.Error(err, "Failed to read XCCDF file")
		os.Exit(1)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		serverUploadErr := uploadToResultServer(arfContents, scapresultsconf)
		if serverUploadErr != nil {
			log.Error(serverUploadErr, "Failed to upload results to server")
			os.Exit(1)
		}
		log.Info("Uploaded to resultserver")
		wg.Done()
	}()

	go func() {
		cmUploadErr := uploadResultConfigMap(xccdfContents, exitcode, scapresultsconf, client)
		if cmUploadErr != nil {
			log.Error(cmUploadErr, "Failed to upload ConfigMap")
			os.Exit(1)
		}
		log.Info("Uploaded ConfigMap")
		wg.Done()
	}()
	wg.Wait()
}

func handleErrorInOscapRun(exitcode string, scapresultsconf *scapresultsConfig, client *complianceCrClient) {
	errorMsg, err := readResultsFile(scapresultsconf.CmdOutputFile, scapresultsconf.Timeout)
	if err != nil {
		log.Error(err, "Failed to read error message output from oscap run")
		os.Exit(1)
	}

	err = uploadErrorConfigMap(errorMsg, exitcode, scapresultsconf, client)
	if err != nil {
		log.Error(err, "Failed to upload error ConfigMap")
		os.Exit(1)
	}
	log.Info("Uploaded ConfigMap")
}

func getOscapExitCode(scapresultsconf *scapresultsConfig) string {
	exitcodeContent, err := readResultsFile(scapresultsconf.ExitCodeFile, scapresultsconf.Timeout)
	if err != nil {
		log.Error(err, "Failed to read oscap error code")
		os.Exit(1)
	}

	if len(exitcodeContent.contents) < 1 {
		log.Error(fmt.Errorf("error code file can't be empty"), "exitcode file was empty")
		os.Exit(1)
	}

	return string(exitcodeContent.contents[0])
}

func getMutualHttpsTransport(c *scapresultsConfig) (*http.Transport, error) {
	cert, err := tls.LoadX509KeyPair(c.Cert, c.Key)
	if err != nil {
		return nil, err
	}
	ca, err := ioutil.ReadFile(c.CA)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)

	tlsConfig := &tls.Config{}
	// Configures TLS 1.2
	tlsConfig = libgocrypto.SecureTLSConfig(tlsConfig)
	tlsConfig.RootCAs = pool
	tlsConfig.Certificates = []tls.Certificate{cert}

	return &http.Transport{
		TLSClientConfig: tlsConfig,
	}, nil
}

// an exit code of 0 means that the scan returned compliant
// an exit code of 2 means that the scan returned non-compliant
// an exit code of 1 means that the scan encountered an error
func exitCodeIsError(exitcode string) bool {
	return exitcode != common.OpenSCAPExitCodeCompliant && exitcode != common.OpenSCAPExitCodeNonCompliant
}

func resultCollectorMain(cmd *cobra.Command, args []string) {
	scapresultsconf := parseConfig(cmd)

	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	crclient, err := createCrClient(cfg)
	if err != nil {
		log.Error(err, "Cannot create kube client for our types\n")
		os.Exit(1)
	}

	exitcode := getOscapExitCode(scapresultsconf)
	log.Info("Got exit-code from file", "exit-code", exitcode)

	if exitCodeIsError(exitcode) {
		handleErrorInOscapRun(exitcode, scapresultsconf, crclient)
		return
	}
	handleCompleteSCAPResults(exitcode, scapresultsconf, crclient)
}
