/*
Copyright © 2020 Red Hat Inc.

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
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	backoff "github.com/cenkalti/backoff/v3"
	"github.com/dsnet/compress/bzip2"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/utils"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	configMapRemediationsProcessed = "compliance-remediations/processed"
	configMapCompressed            = "openscap-scan-result/compressed"
	maxRetries                     = 15
)

type aggregatorConfig struct {
	Content   string
	ScanName  string
	Namespace string
}

type complianceCrClient struct {
	client runtimeclient.Client
	scheme *runtime.Scheme
}

func defineFlags(cmd *cobra.Command) {
	cmd.Flags().String("content", "", "The path to the OpenScap content")
	cmd.Flags().String("scan", "", "The compliance scan that owns the configMap objects.")
	cmd.Flags().String("namespace", "openshift-compliance", "Running pod namespace.")
}

func parseConfig(cmd *cobra.Command) *aggregatorConfig {
	var conf aggregatorConfig
	conf.Content = getValidStringArg(cmd, "content")
	conf.ScanName = getValidStringArg(cmd, "scan")
	conf.Namespace = getValidStringArg(cmd, "namespace")
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

func createCrClient(config *rest.Config) (*complianceCrClient, error) {
	scheme := runtime.NewScheme()

	v1.AddToScheme(scheme)
	mcfgv1.AddToScheme(scheme)

	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&compv1alpha1.ComplianceRemediation{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&compv1alpha1.ComplianceScan{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&compv1alpha1.ComplianceCheckResult{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&metav1.CreateOptions{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&metav1.UpdateOptions{})

	client, err := runtimeclient.New(config, runtimeclient.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, err
	}

	return &complianceCrClient{
		client: client,
		scheme: scheme,
	}, nil
}

func getScanConfigMaps(clientset *kubernetes.Clientset, scan, namespace string) ([]v1.ConfigMap, error) {
	var cMapList *v1.ConfigMapList
	var err error

	// Look for configMap with this scan label
	listOpts := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", compv1alpha1.ComplianceScanIndicatorLabel, scan),
	}

	cMapList, err = clientset.CoreV1().ConfigMaps(namespace).List(listOpts)
	if err != nil {
		fmt.Printf("Error waiting for CMs of scan %s: %v\n", scan, err)
		return nil, err
	}

	if len(cMapList.Items) == 0 {
		fmt.Printf("Scan %s has no results\n", scan)
		return make([]v1.ConfigMap, 0), nil
	}

	fmt.Printf("Scan %s has %d results\n", scan, len(cMapList.Items))
	return cMapList.Items, nil
}

func readCompressedData(compressed string) (*bzip2.Reader, error) {
	decoded, err := base64.StdEncoding.DecodeString(compressed)
	if err != nil {
		return nil, err
	}

	compressedReader := bytes.NewReader(decoded)
	return bzip2.NewReader(compressedReader, &bzip2.ReaderConfig{})
}

func parseResultRemediations(scheme *runtime.Scheme, scanName, namespace string, content *utils.XMLDocument, cm *v1.ConfigMap) ([]*utils.ParseResult, error) {
	var scanReader io.Reader

	_, ok := cm.Annotations[configMapRemediationsProcessed]
	if ok {
		fmt.Printf("ConfigMap %s already processed\n", cm.Name)
		return nil, nil
	}

	cmScanResult, ok := cm.Data["results"]
	if !ok {
		return nil, fmt.Errorf("no results in configmap %s", cm.Name)
	}

	_, ok = cm.Annotations[configMapCompressed]
	if ok {
		fmt.Printf("Results are compressed\n")
		scanResult, err := readCompressedData(cmScanResult)
		if err != nil {
			return nil, err
		}
		defer scanResult.Close()
		scanReader = scanResult
	} else {
		scanReader = strings.NewReader(cmScanResult)
	}

	return utils.ParseResultsFromContentAndXccdf(scheme, scanName, namespace, content, scanReader)
}

func annotateParsedConfigMap(clientset *kubernetes.Clientset, cm *v1.ConfigMap) error {
	cmCopy := cm.DeepCopy()

	if cmCopy.Annotations == nil {
		cmCopy.Annotations = make(map[string]string)
	}
	cmCopy.Annotations[configMapRemediationsProcessed] = ""

	err := backoff.Retry(func() error {
		_, err := clientset.CoreV1().ConfigMaps(cmCopy.Namespace).Update(cmCopy)
		return err
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
	return err
}

type compResultIface interface {
	metav1.Object
	runtime.Object
}

func createOrUpdateOneResult(crClient *complianceCrClient, owner metav1.Object, labels map[string]string, exists bool, res compResultIface) error {
	kind := res.GetObjectKind()

	if err := controllerutil.SetControllerReference(owner, res, crClient.scheme); err != nil {
		fmt.Printf("Failed to set '%s' ownership %v", kind.GroupVersionKind().Kind, err)
		return err
	}

	res.SetLabels(labels)

	name := res.GetName()
	fmt.Printf("Creating %s:%s\n", kind.GroupVersionKind().Kind, name)

	err := backoff.Retry(func() error {
		var err error
		if !exists {
			err = crClient.client.Create(context.TODO(), res)
		} else {
			err = crClient.client.Update(context.TODO(), res)
		}
		if err != nil && !errors.IsAlreadyExists(err) {
			fmt.Printf("Retrying with a backoff because of an error while creating or updating object: %v\n", err)
			return err
		}
		return nil
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
	if err != nil {
		fmt.Printf("Failed to create a '%s' object: %v\n", kind.GroupVersionKind().Kind, err)
		return err
	}
	return nil
}

func getRemediationLabels(scan *compv1alpha1.ComplianceScan) (map[string]string, error) {
	labels := make(map[string]string)
	labels[compv1alpha1.ScanLabel] = scan.Name
	labels[compv1alpha1.SuiteLabel] = scan.Labels[compv1alpha1.SuiteLabel]
	labels[mcfgv1.MachineConfigRoleLabelKey] = utils.GetFirstNodeRole(scan.Spec.NodeSelector)
	if labels[mcfgv1.MachineConfigRoleLabelKey] == "" {
		return nil, fmt.Errorf("scan %s has no role assignment", scan.Name)
	}

	return labels, nil
}

func getCheckResultLabels(cr *compv1alpha1.ComplianceCheckResult, scan *compv1alpha1.ComplianceScan) map[string]string {
	labels := make(map[string]string)
	labels[compv1alpha1.ScanLabel] = scan.Name
	labels[compv1alpha1.SuiteLabel] = scan.Labels[compv1alpha1.SuiteLabel]
	labels[compv1alpha1.ComplianceCheckResultStatusLabel] = string(cr.Status)
	labels[compv1alpha1.ComplianceCheckResultSeverityLabel] = string(cr.Severity)

	return labels
}

func createResults(crClient *complianceCrClient, scan *compv1alpha1.ComplianceScan, parsedResults []*utils.ParseResult) error {
	fmt.Printf("Will create %d result objects\n", len(parsedResults))
	if len(parsedResults) == 0 {
		fmt.Println("Nothing to create")
		return nil
	}

	for _, pr := range parsedResults {
		if pr == nil || pr.CheckResult == nil {
			fmt.Println("nil result or result.check, this shouldn't happen")
			continue
		}

		checkResultLabels := getCheckResultLabels(pr.CheckResult, scan)
		if checkResultLabels == nil {
			return fmt.Errorf("cannot create checkResult labels")
		}

		crkey := getObjKey(pr.CheckResult.GetName(), pr.CheckResult.GetNamespace())
		foundCheckResult := &compv1alpha1.ComplianceCheckResult{}
		// Copy type metadata so dynamic client copies data correctly
		foundCheckResult.TypeMeta = pr.CheckResult.TypeMeta
		checkResultExists, err := getObjectIfFound(crClient, crkey, foundCheckResult)
		if checkResultExists {
			// Copy resource version and other metadata needed for update
			foundCheckResult.ObjectMeta.DeepCopyInto(&pr.CheckResult.ObjectMeta)
		}
		// check is owned by the scan
		if err := createOrUpdateOneResult(crClient, scan, checkResultLabels, checkResultExists, pr.CheckResult); err != nil {
			return fmt.Errorf("cannot create or update checkResult %s: %v", pr.CheckResult.Name, err)
		}

		if pr.Remediation == nil {
			continue
		}

		remLabels, err := getRemediationLabels(scan)
		if err != nil {
			return err
		}

		remkey := getObjKey(pr.Remediation.GetName(), pr.Remediation.GetNamespace())
		foundRemediation := &compv1alpha1.ComplianceRemediation{}
		// Copy type metadata so dynamic client copies data correctly
		foundRemediation.TypeMeta = pr.Remediation.TypeMeta
		remExists, err := getObjectIfFound(crClient, remkey, foundRemediation)
		if remExists {
			// Copy resource version and other metadata needed for update
			foundRemediation.ObjectMeta.DeepCopyInto(&pr.Remediation.ObjectMeta)
		}
		// remediation is owned by the check
		if err := createOrUpdateOneResult(crClient, pr.CheckResult, remLabels, remExists, pr.Remediation); err != nil {
			return fmt.Errorf("cannot create or update remediation %s: %v", pr.Remediation.Name, err)
		}

	}

	return nil
}

func getObjKey(name, ns string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: ns}
}

// Returns whether or not an object exists, and updates the data in the obj.
// If there is an error that is not acceptable, it'll be returned
func getObjectIfFound(crClient *complianceCrClient, key types.NamespacedName, obj runtime.Object) (bool, error) {
	var found bool
	err := backoff.Retry(func() error {
		err := crClient.client.Get(context.TODO(), key, obj)
		if errors.IsNotFound(err) {
			return nil
		} else if err != nil {
			fmt.Printf("Retrying with a backoff because of an error while getting object: %v\n", err)
			return err
		}
		found = true
		return nil
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))

	return found, err
}

func readContent(filename string) (*os.File, error) {
	// gosec complains that the file is passed through an evironment variable. But
	// this is not a security issue because none of the files are user-provided
	cleanFileName := filepath.Clean(filename)
	// #nosec G304
	return os.Open(cleanFileName)
}

func aggregator(cmd *cobra.Command, args []string) {
	var scanParsedResults []*utils.ParseResult

	aggregatorConf := parseConfig(cmd)

	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Printf("Can't create incluster config: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Cannot create clientset: %v\n", err)
		os.Exit(1)
	}

	crclient, err := createCrClient(config)
	if err != nil {
		fmt.Printf("Cannot create client for our types: %v\n", err)
		os.Exit(1)
	}

	var scan = &compv1alpha1.ComplianceScan{}
	err = crclient.client.Get(context.TODO(), types.NamespacedName{
		Namespace: aggregatorConf.Namespace,
		Name:      aggregatorConf.ScanName,
	}, scan)
	if err != nil {
		fmt.Printf("Cannot retrieve the scan instance: %v\n", err)
		os.Exit(1)
	}

	if scan.Status.Result == compv1alpha1.ResultError {
		fmt.Println("Not gathering results from a scan that resulted in an error")
		os.Exit(0)
	}

	// Find all the configmaps for a scan
	configMaps, err := getScanConfigMaps(clientset, aggregatorConf.ScanName, aggregatorConf.Namespace)
	if err != nil {
		fmt.Printf("getScanConfigMaps failed: %v\n", err)
		os.Exit(1)
	}

	contentFile, err := readContent(aggregatorConf.Content)
	if err != nil {
		fmt.Printf("Cannot read the content: %v\n", err)
		os.Exit(1)
	}
	// #nosec
	defer contentFile.Close()
	bufContentFile := bufio.NewReader(contentFile)
	contentDom, err := utils.ParseContent(bufContentFile)
	if err != nil {
		fmt.Printf("Cannot parse the content: %v\n", err)
		os.Exit(1)
	}

	// For each configmap, create a list of remediations
	for _, cm := range configMaps {
		fmt.Printf("processing CM: %s\n", cm.Name)

		cmParsedResults, err := parseResultRemediations(crclient.scheme, aggregatorConf.ScanName, aggregatorConf.Namespace, contentDom, &cm)
		if err != nil {
			fmt.Printf("Cannot parse CM %s into remediations, err: %v\n", cm.Name, err)
		} else if cmParsedResults == nil {
			fmt.Println("Either no parsed results found in result or result already processed")
			continue
		}
		fmt.Printf("CM %s contained %d parsed results\n", cm.Name, len(cmParsedResults))

		// If there are any results, make sure all of them for the scan are
		// exactly the same
		if scanParsedResults == nil {
			// This is the first loop or only result
			fmt.Println("This is the first remediation list, keeping it")
			scanParsedResults = cmParsedResults
		} else {
			// All remediation lists in the scan must be equal
			ok := utils.DiffRemediationList(scanParsedResults, cmParsedResults)
			if !ok {
				fmt.Println("The remediations differ between machines, this should never happen as the machines in a pool should be identical")
				os.Exit(1)
			}
		}
	}

	// At this point either scanRemediations is nil or contains a list
	// of remediations for this scan
	// Create the remediations
	fmt.Println("Creating result objects")
	if err := createResults(crclient, scan, scanParsedResults); err != nil {
		fmt.Printf("Could not create remediation objects: %v\n", err)
		os.Exit(1)
	}

	// Annotate configMaps, so we don't need to re-parse them
	fmt.Println("Annotating ConfigMaps")
	for _, cm := range configMaps {
		err = annotateParsedConfigMap(clientset, &cm)
		if err != nil {
			fmt.Printf("Cannot annotate the CM: %v\n", err)
			os.Exit(1)
		}
	}
}

var rootCmd = &cobra.Command{
	Use:   "aggregator",
	Short: "Aggregate configMaps complianceRemediations",
	Long:  "A tool to aggregate configMaps with scan results to complianceRemediation types",
	Run:   aggregator,
}

func main() {
	defineFlags(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	os.Exit(0)
}
