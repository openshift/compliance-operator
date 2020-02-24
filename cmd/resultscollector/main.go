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
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	crdGroup      = "complianceoperator.compliance.openshift.io"
	crdAPIVersion = "v1alpha1"
	crdPlurals    = "compliancescans"
	maxRetries    = 15
)

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
	Compress        bool
	Cert            string
	Key             string
	CA              string
}

func defineFlags(cmd *cobra.Command) {
	cmd.Flags().String("arf-file", "", "The ARF file to watch.")
	cmd.Flags().String("results-file", "", "The XCCDF results file to watch.")
	cmd.Flags().String("exit-code-file", "", "A file containing the oscap command's exit code.")
	cmd.Flags().String("oscap-output-file", "", "A file containing the oscap command's output.")
	cmd.Flags().String("owner", "", "The compliance scan that owns the configMap objects.")
	cmd.Flags().String("config-map-name", "", "The configMap to upload to, typically the podname.")
	cmd.Flags().String("namespace", "openshift-compliance", "Running pod namespace.")
	cmd.Flags().Int64("timeout", 3600, "How long to wait for the file.")
	cmd.Flags().Bool("compress", false, "Always compress the results.")
	cmd.Flags().String("resultserveruri", "", "The resultserver URI name.")
	cmd.Flags().String("tls-client-cert", "", "The path to the client and CA PEM cert bundle.")
	cmd.Flags().String("tls-client-key", "", "The path to the client PEM key.")
	cmd.Flags().String("tls-ca", "", "The path to the CA certificate.")
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
	conf.Compress, _ = cmd.Flags().GetBool("compress")
	conf.ResultServerURI, _ = cmd.Flags().GetString("resultserveruri")
	// Set default if needed
	if conf.ResultServerURI == "" {
		conf.ResultServerURI = "http://" + conf.ScanName + "-rs:8080/"
	}
	return &conf
}

func getValidStringArg(cmd *cobra.Command, name string) string {
	val, _ := cmd.Flags().GetString(name)
	if val == "" {
		fmt.Fprintf(os.Stderr, "The command line argument '%s' is mandatory.\n", name)
		os.Exit(1)
	}
	return val
}

func getOpenSCAPScanInstance(name, namespace string, dynclient dynamic.Interface) (*unstructured.Unstructured, error) {
	openscapScanRes := schema.GroupVersionResource{
		Group:    crdGroup,
		Version:  crdAPIVersion,
		Resource: crdPlurals,
	}

	openscapScan, err := dynclient.Resource(openscapScanRes).Namespace(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	return openscapScan, nil
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

func readResultsFile(filename string, timeout int64, doCompress bool) (*resultFileContents, error) {
	var err error
	var rfContents resultFileContents

	handle := waitForResultsFile(filename, timeout)
	defer handle.Close()

	rfContents.contents, err = ioutil.ReadAll(handle)
	if err != nil {
		return nil, err
	}

	if resultNeedsCompression(rfContents.contents) || doCompress {
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

func getConfigMap(owner *unstructured.Unstructured, configMapName, filename string, contents []byte, compressed bool, exitcode string) *corev1.ConfigMap {
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
			Annotations: annotations,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: owner.GetAPIVersion(),
					Kind:       owner.GetKind(),
					Name:       owner.GetName(),
					UID:        owner.GetUID(),
				},
			},
			Labels: map[string]string{
				"compliance-scan": owner.GetName(),
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
	scapresultsconf *scapresultsConfig, clientset *kubernetes.Clientset, dynclient dynamic.Interface) error {
	return backoff.Retry(func() error {
		fmt.Println("Trying to upload results ConfigMap")
		openscapScan, err := getOpenSCAPScanInstance(scapresultsconf.ScanName, scapresultsconf.Namespace, dynclient)
		if err != nil {
			return err
		}
		confMap := getConfigMap(openscapScan, scapresultsconf.ConfigMapName, "results", xccdfContents.contents, xccdfContents.compressed, exitcode)
		_, err = clientset.CoreV1().ConfigMaps(scapresultsconf.Namespace).Create(confMap)

		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
}

func uploadErrorConfigMap(errorMsg *resultFileContents, exitcode string,
	scapresultsconf *scapresultsConfig, clientset *kubernetes.Clientset, dynclient dynamic.Interface) error {
	return backoff.Retry(func() error {
		fmt.Println("Trying to upload error ConfigMap")
		openscapScan, err := getOpenSCAPScanInstance(scapresultsconf.ScanName, scapresultsconf.Namespace, dynclient)
		if err != nil {
			return err
		}
		confMap := getConfigMap(openscapScan, scapresultsconf.ConfigMapName, "error-msg", errorMsg.contents, errorMsg.compressed, exitcode)
		_, err = clientset.CoreV1().ConfigMaps(scapresultsconf.Namespace).Create(confMap)

		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
}

func handleCompleteSCAPResults(exitcode string, scapresultsconf *scapresultsConfig,
	clientset *kubernetes.Clientset, dynclient dynamic.Interface) {
	arfContents, err := readResultsFile(scapresultsconf.ArfFile, scapresultsconf.Timeout, scapresultsconf.Compress)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	xccdfContents, err := readResultsFile(scapresultsconf.XccdfFile, scapresultsconf.Timeout, scapresultsconf.Compress)
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
		err = uploadResultConfigMap(xccdfContents, exitcode, scapresultsconf, clientset, dynclient)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("Uploaded ConfigMap")
		wg.Done()
	}()
	wg.Wait()
}

func handleErrorInOscapRun(exitcode string, scapresultsconf *scapresultsConfig,
	clientset *kubernetes.Clientset, dynclient dynamic.Interface) {
	errorMsg, err := readResultsFile(scapresultsconf.CmdOutputFile, scapresultsconf.Timeout, false)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	err = uploadErrorConfigMap(errorMsg, exitcode, scapresultsconf, clientset, dynclient)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Uploaded ConfigMap")
}

func getOscapExitCode(scapresultsconf *scapresultsConfig) string {
	exitcodeContent, err := readResultsFile(scapresultsconf.ExitCodeFile, scapresultsconf.Timeout, false)
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

	// TODO: Configure cipher suites. Perhaps use library-go helper functions.
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:      pool,
			Certificates: []tls.Certificate{cert},
		},
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

	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	dynclient, err := dynamic.NewForConfig(config)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	exitcode := getOscapExitCode(scapresultsconf)
	fmt.Printf("Got exit-code \"%s\" from file.\n", exitcode)

	if exitCodeIsError(exitcode) {
		handleErrorInOscapRun(exitcode, scapresultsconf, clientset, dynclient)
		return
	}
	handleCompleteSCAPResults(exitcode, scapresultsconf, clientset, dynclient)
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "resultscollector",
		Short: "A tool to do an OpenSCAP scan from a pod.",
		Long:  "A tool to do an OpenSCAP scan from a pod.",
		Run:   resultCollectorMain,
	}

	defineFlags(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
