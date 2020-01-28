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
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	backoff "github.com/cenkalti/backoff/v3"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
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
	File          string
	ScanName      string
	ConfigMapName string
	Namespace     string
	Timeout       int64
	Compress      bool
}

func defineFlags(cmd *cobra.Command) {
	cmd.Flags().String("file", "", "The file to watch.")
	cmd.Flags().String("owner", "", "The compliance scan that owns the configMap objects.")
	cmd.Flags().String("config-map-name", "", "The configMap to upload to, typically the podname.")
	cmd.Flags().String("namespace", "Running pod namespace.", ".")
	cmd.Flags().Int64("timeout", 3600, "How long to wait for the file.")
	cmd.Flags().Bool("compress", false, "Always compress the results.")
}

func parseConfig(cmd *cobra.Command) *scapresultsConfig {
	var conf scapresultsConfig
	conf.File = getValidStringArg(cmd, "file")
	conf.ScanName = getValidStringArg(cmd, "owner")
	conf.ConfigMapName = getValidStringArg(cmd, "config-map-name")
	conf.Namespace = getValidStringArg(cmd, "namespace")
	conf.Timeout, _ = cmd.Flags().GetInt64("timeout")
	conf.Compress, _ = cmd.Flags().GetBool("compress")
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

func compressResults(contents []byte) []byte {
	// Encode the contents ascii, compress it with gzip, b64encode it so it
	// can be stored in the configmap.
	var buffer bytes.Buffer
	w := gzip.NewWriter(&buffer)
	w.Write([]byte(contents))
	w.Close()
	return []byte(base64.StdEncoding.EncodeToString(buffer.Bytes()))
}

func getConfigMap(owner *unstructured.Unstructured, configMapName, filename string, contents []byte, compressed bool) *corev1.ConfigMap {
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
				metav1.OwnerReference{
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
			filename: string(contents),
		},
	}
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "resultscollector",
		Short: "A tool to do an OpenSCAP scan from a pod.",
		Long:  "A tool to do an OpenSCAP scan from a pod.",
		Run: func(cmd *cobra.Command, args []string) {
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
			file := waitForResultsFile(scapresultsconf.File, scapresultsconf.Timeout)
			defer file.Close()

			contents, err := ioutil.ReadAll(file)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			compressed := false
			if resultNeedsCompression(contents) || scapresultsconf.Compress {
				contents = compressResults(contents)
				compressed = true
				fmt.Println("Needs compression.")
			}

			err = backoff.Retry(func() error {
				openscapScan, err := getOpenSCAPScanInstance(scapresultsconf.ScanName, scapresultsconf.Namespace, dynclient)
				if err != nil {
					return err
				}
				confMap := getConfigMap(openscapScan, scapresultsconf.ConfigMapName, "results", contents, compressed)
				_, err = clientset.CoreV1().ConfigMaps(scapresultsconf.Namespace).Create(confMap)
				return err
			}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))

			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		},
	}

	defineFlags(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
