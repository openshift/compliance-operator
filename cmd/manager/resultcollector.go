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
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"sync"
	"time"

	backoff "github.com/cenkalti/backoff/v3"
	"github.com/dsnet/compress/bzip2"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
)

const (
	crdGroup      = "compliance.openshift.io"
	crdAPIVersion = "v1alpha1"
	crdPlurals    = "compliancescans"
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
	ArfFile         string
	XccdfFile       string
	ExitCodeFile    string
	CmdOutputFile   string
	ScanName        string
	ConfigMapName   string
	Namespace       string
	ResultServerURI string
	Timeout         int64
	Cert            string
	Key             string
	CA              string
}

func defineResultcollectorFlags(cmd *cobra.Command) {
	cmd.Flags().String("arf-file", "", "The ARF file to watch.")
	cmd.Flags().String("results-file", "", "The XCCDF results file to watch.")
	cmd.Flags().String("exit-code-file", "", "A file containing the oscap command's exit code.")
	cmd.Flags().String("oscap-output-file", "", "A file containing the oscap command's output.")
	cmd.Flags().String("owner", "", "The compliance scan that owns the configMap objects.")
	cmd.Flags().String("config-map-name", "", "The configMap to upload to, typically the podname.")
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
	return &conf
}

func getOpenSCAPScanInstance(name, namespace string, client *complianceCrClient) (*compv1alpha1.ComplianceScan, error) {
	key := types.NamespacedName{Name: name, Namespace: namespace}
	scan := &compv1alpha1.ComplianceScan{}
	err := client.client.Get(context.TODO(), key, scan)
	if err != nil {
		fmt.Println(err)
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
				fmt.Println(err)
				os.Exit(1)
			}
			time.Sleep(1 * time.Second)
		}
	}()

	select {
	case file := <-readFileTimeoutChan:
		fmt.Printf("File '%s' found, will upload it.\n", filename)
		return file
	case <-time.After(time.Duration(timeout) * time.Second):
		fmt.Println("Timeout. Aborting.")
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
	return []byte(base64.StdEncoding.EncodeToString(buffer.Bytes())), nil
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
		fmt.Printf("%s Needs compression\n", filename)
		if err != nil {
			fmt.Println("Error: Compression failed")
			return nil, err
		}
		rfContents.compressed = true
		fmt.Printf("Compressed results are %d bytes in size\n", len(rfContents.contents))
	}

	return &rfContents, nil
}

func getConfigMap(owner metav1.Object, configMapName, filename string, contents []byte, compressed bool, exitcode string) *corev1.ConfigMap {
	annotations := map[string]string{}
	if compressed {
		annotations = map[string]string{
			"openscap-scan-result/compressed": "",
		}
	}

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        configMapName,
			Namespace:   common.GetComplianceOperatorNamespace(),
			Annotations: annotations,
			Labels: map[string]string{
				compv1alpha1.ComplianceScanIndicatorLabel: owner.GetName(),
			},
		},
		Data: map[string]string{
			"exit-code": exitcode,
			filename:    string(contents),
		},
	}
}

func uploadToResultServer(arfContents *resultFileContents, scapresultsconf *scapresultsConfig) error {
	return backoff.Retry(func() error {
		url := scapresultsconf.ResultServerURI
		fmt.Printf("Trying to upload to resultserver: %s\n", url)
		reader := bytes.NewReader(arfContents.contents)
		transport, err := getMutualHttpsTransport(scapresultsconf)
		if err != nil {
			fmt.Println(err)
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
			fmt.Println(err)
			return err
		}
		defer resp.Body.Close()
		bytesresp, err := httputil.DumpResponse(resp, true)
		if err != nil {
			fmt.Println(err)
			return err
		}
		fmt.Println(string(bytesresp))
		return nil
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
}

func uploadResultConfigMap(xccdfContents *resultFileContents, exitcode string,
	scapresultsconf *scapresultsConfig, client *complianceCrClient) error {
	return backoff.Retry(func() error {
		fmt.Println("Trying to upload results ConfigMap")
		openscapScan, err := getOpenSCAPScanInstance(scapresultsconf.ScanName, scapresultsconf.Namespace, client)
		if err != nil {
			return err
		}
		confMap := getConfigMap(openscapScan, scapresultsconf.ConfigMapName, "results", xccdfContents.contents, xccdfContents.compressed, exitcode)
		err = client.client.Create(context.TODO(), confMap)

		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
}

func uploadErrorConfigMap(errorMsg *resultFileContents, exitcode string,
	scapresultsconf *scapresultsConfig, client *complianceCrClient) error {
	return backoff.Retry(func() error {
		fmt.Println("Trying to upload error ConfigMap")
		openscapScan, err := getOpenSCAPScanInstance(scapresultsconf.ScanName, scapresultsconf.Namespace, client)
		if err != nil {
			return err
		}
		confMap := getConfigMap(openscapScan, scapresultsconf.ConfigMapName, "error-msg", errorMsg.contents, errorMsg.compressed, exitcode)
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
		fmt.Println(err)
		os.Exit(1)
	}

	xccdfContents, err := readResultsFile(scapresultsconf.XccdfFile, scapresultsconf.Timeout)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		err = uploadToResultServer(arfContents, scapresultsconf)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("Uploaded to resultserver")
		wg.Done()
	}()

	go func() {
		err = uploadResultConfigMap(xccdfContents, exitcode, scapresultsconf, client)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("Uploaded ConfigMap")
		wg.Done()
	}()
	wg.Wait()
}

func handleErrorInOscapRun(exitcode string, scapresultsconf *scapresultsConfig, client *complianceCrClient) {
	errorMsg, err := readResultsFile(scapresultsconf.CmdOutputFile, scapresultsconf.Timeout)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	err = uploadErrorConfigMap(errorMsg, exitcode, scapresultsconf, client)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Uploaded ConfigMap")
}

func getOscapExitCode(scapresultsconf *scapresultsConfig) string {
	exitcodeContent, err := readResultsFile(scapresultsconf.ExitCodeFile, scapresultsconf.Timeout)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if len(exitcodeContent.contents) < 1 {
		fmt.Println("exitcode file was empty")
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
	return exitcode != "0" && exitcode != "2"
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
		fmt.Printf("Cannot create client for our types: %v\n", err)
		os.Exit(1)
	}

	exitcode := getOscapExitCode(scapresultsconf)
	fmt.Printf("Got exit-code \"%s\" from file.\n", exitcode)

	if exitCodeIsError(exitcode) {
		handleErrorInOscapRun(exitcode, scapresultsconf, crclient)
		return
	}
	handleCompleteSCAPResults(exitcode, scapresultsconf, crclient)
}
