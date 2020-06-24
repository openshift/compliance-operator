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
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	backoff "github.com/cenkalti/backoff/v3"
	"github.com/dsnet/compress/bzip2"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	configMapRemediationsProcessed = "compliance-remediations/processed"
	configMapCompressed            = "openscap-scan-result/compressed"
)

var aggregatorCmd = &cobra.Command{
	Use:   "aggregator",
	Short: "Aggregate configMaps complianceRemediations",
	Long:  "A tool to aggregate configMaps with scan results to complianceRemediation types",
	Run:   aggregator,
}

func init() {
	rootCmd.AddCommand(aggregatorCmd)
	defineAggregatorFlags(aggregatorCmd)
}

type aggregatorConfig struct {
	Content   string
	ScanName  string
	Namespace string
}

func defineAggregatorFlags(cmd *cobra.Command) {
	cmd.Flags().String("content", "", "The path to the OpenScap content")
	cmd.Flags().String("scan", "", "The compliance scan that owns the configMap objects.")
	cmd.Flags().String("namespace", "openshift-compliance", "Running pod namespace.")

	flags := cmd.Flags()
	flags.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	flags.AddGoFlagSet(flag.CommandLine)
}

func parseAggregatorConfig(cmd *cobra.Command) *aggregatorConfig {
	var conf aggregatorConfig
	conf.Content = getValidStringArg(cmd, "content")
	conf.ScanName = getValidStringArg(cmd, "scan")
	conf.Namespace = getValidStringArg(cmd, "namespace")
	return &conf
}

func getScanConfigMaps(crClient *complianceCrClient, scan, namespace string) ([]v1.ConfigMap, error) {
	cMapList := &v1.ConfigMapList{}
	var err error

	// Look for configMap with this scan label
	inNs := client.InNamespace(namespace)
	withLabel := client.MatchingLabels{compv1alpha1.ComplianceScanIndicatorLabel: scan}

	err = crClient.client.List(context.TODO(), cMapList, inNs, withLabel)
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

// parseResultRemediations parses scan results from a configMap with the help of DS provided in the
// content parameter.
// Returns a triple of (array-of-ParseResults, source, error) where source identifies the entity whose
// scan produced this configMap -- typically a nodeName for node scans. For platform scans, the source
// is empty. The source is used later when reconciling inconsistent results
func parseResultRemediations(scheme *runtime.Scheme, scanName, namespace string, content *utils.XMLDocument, cm *v1.ConfigMap) ([]*utils.ParseResult, string, error) {
	var scanReader io.Reader

	_, ok := cm.Annotations[configMapRemediationsProcessed]
	if ok {
		fmt.Printf("ConfigMap %s already processed\n", cm.Name)
		return nil, "", nil
	}

	cmScanResult, ok := cm.Data["results"]
	if !ok {
		return nil, "", fmt.Errorf("no results in configmap %s", cm.Name)
	}

	_, ok = cm.Annotations[configMapCompressed]
	if ok {
		fmt.Printf("Results are compressed\n")
		scanResult, err := readCompressedData(cmScanResult)
		if err != nil {
			return nil, "", err
		}
		defer scanResult.Close()
		scanReader = scanResult
	} else {
		scanReader = strings.NewReader(cmScanResult)
	}

	// This would return an empty string for a platform check that is handled later explicitly
	nodeName := cm.Annotations["openscap-scan-result/node"]

	table, err := utils.ParseResultsFromContentAndXccdf(scheme, scanName, namespace, content, scanReader)
	return table, nodeName, err
}

func getScanResult(cm *v1.ConfigMap) (compv1alpha1.ComplianceScanStatusResult, string) {
	exitcode, ok := cm.Data["exit-code"]
	if ok {
		switch exitcode {
		case "0":
			return compv1alpha1.ResultCompliant, ""
		case "2":
			return compv1alpha1.ResultNonCompliant, ""
		default:
			errorMsg, ok := cm.Data["error-msg"]
			if ok {
				return compv1alpha1.ResultError, errorMsg
			}
			return compv1alpha1.ResultError, fmt.Sprintf("The ConfigMap '%s' was missing 'error-msg'", cm.Name)
		}
	}
	return compv1alpha1.ResultError, fmt.Sprintf("The ConfigMap '%s' was missing 'exit-code'", cm.Name)
}

func annotateCMWithScanResult(cm *v1.ConfigMap, cmParsedResults []*utils.ParseResult) *v1.ConfigMap {
	scanResult, errMsg := getScanResult(cm)
	if scanResult == compv1alpha1.ResultCompliant {
		// Special case: If the OS didn't match at all and SCAP skipped all the tests,
		// then we would have gotten COMPLIANT. Let's make sure that at least one
		// rule passed in this case
		gotPass := false
		for i := range cmParsedResults {
			if cmParsedResults[i] == nil || cmParsedResults[i].CheckResult == nil {
				continue
			}

			if cmParsedResults[i].CheckResult.Status == compv1alpha1.CheckResultPass {
				gotPass = true
				break
			}
		}

		if gotPass == false {
			scanResult = compv1alpha1.ResultNotApplicable
			errMsg = "The scan did not produce any results, maybe an OS/platform mismatch?"
		}
	}

	// Finally annotate the CM with the result. The CM will be deep-copied prior to the
	// update anyway
	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	cm.Annotations[compv1alpha1.CmScanResultAnnotation] = string(scanResult)
	cm.Annotations[compv1alpha1.CmScanResultErrMsg] = errMsg
	return cm.DeepCopy()
}

func markConfigMapAsProcessed(crClient *complianceCrClient, cm *v1.ConfigMap) error {
	cmCopy := cm.DeepCopy()

	if cmCopy.Annotations == nil {
		cmCopy.Annotations = make(map[string]string)
	}
	cmCopy.Annotations[configMapRemediationsProcessed] = ""

	err := backoff.Retry(func() error {
		return crClient.client.Update(context.TODO(), cmCopy)
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
	return err
}

type compResultIface interface {
	metav1.Object
	runtime.Object
}

func createOrUpdateOneResult(crClient *complianceCrClient, owner metav1.Object, labels map[string]string, annotations map[string]string, exists bool, res compResultIface) error {
	kind := res.GetObjectKind()

	if err := controllerutil.SetControllerReference(owner, res, crClient.scheme); err != nil {
		fmt.Printf("Failed to set '%s' ownership %v", kind.GroupVersionKind().Kind, err)
		return err
	}

	res.SetLabels(labels)
	if annotations != nil {
		res.SetAnnotations(annotations)
	}

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

func getCheckResultLabels(cr *compv1alpha1.ComplianceCheckResult, resultLabels map[string]string, scan *compv1alpha1.ComplianceScan) map[string]string {
	labels := make(map[string]string)
	labels[compv1alpha1.ScanLabel] = scan.Name
	labels[compv1alpha1.SuiteLabel] = scan.Labels[compv1alpha1.SuiteLabel]
	labels[compv1alpha1.ComplianceCheckResultStatusLabel] = string(cr.Status)
	labels[compv1alpha1.ComplianceCheckResultSeverityLabel] = string(cr.Severity)

	for k, v := range resultLabels {
		labels[k] = v
	}

	return labels
}

func getCheckResultAnnotations(cr *compv1alpha1.ComplianceCheckResult, resultAnnotations map[string]string) map[string]string {
	annotations := make(map[string]string)
	annotations[compv1alpha1.ComplianceCheckResultRuleAnnotation] = cr.IDToDNSFriendlyName()

	for k, v := range resultAnnotations {
		annotations[k] = v
	}

	return annotations
}

func createResults(crClient *complianceCrClient, scan *compv1alpha1.ComplianceScan, consistentResults []*utils.ParseResultContextItem) error {
	fmt.Printf("Will create %d result objects\n", len(consistentResults))
	if len(consistentResults) == 0 {
		fmt.Println("Nothing to create")
		return nil
	}

	for _, pr := range consistentResults {
		if pr == nil || pr.CheckResult == nil {
			fmt.Println("nil result or result.check, this shouldn't happen")
			continue
		}

		checkResultLabels := getCheckResultLabels(pr.CheckResult, pr.Labels, scan)
		checkResultAnnotations := getCheckResultAnnotations(pr.CheckResult, pr.Annotations)

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
		if err := createOrUpdateOneResult(crClient, scan, checkResultLabels, checkResultAnnotations, checkResultExists, pr.CheckResult); err != nil {
			return fmt.Errorf("cannot create or update checkResult %s: %v", pr.CheckResult.Name, err)
		}

		if pr.Remediation == nil ||
			(pr.CheckResult.Status != compv1alpha1.CheckResultFail &&
				pr.CheckResult.Status != compv1alpha1.CheckResultInfo &&
				pr.CheckResult.Status != compv1alpha1.CheckResultInconsistent) {
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
		if err := createOrUpdateOneResult(crClient, pr.CheckResult, remLabels, nil, remExists, pr.Remediation); err != nil {
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

func aggregator(cmd *cobra.Command, args []string) {
	aggregatorConf := parseAggregatorConfig(cmd)

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

	var scan = &compv1alpha1.ComplianceScan{}
	err = crclient.client.Get(context.TODO(), types.NamespacedName{
		Namespace: aggregatorConf.Namespace,
		Name:      aggregatorConf.ScanName,
	}, scan)
	if err != nil {
		fmt.Printf("Cannot retrieve the scan instance: %v\n", err)
		os.Exit(1)
	}

	// Find all the configmaps for a scan
	configMaps, err := getScanConfigMaps(crclient, aggregatorConf.ScanName, common.GetComplianceOperatorNamespace())
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

	prCtx := utils.NewParseResultContext()

	// For each configmap, create a list of remediations
	for i := range configMaps {
		cm := &configMaps[i]
		fmt.Printf("processing CM: %s\n", cm.Name)

		cmParsedResults, source, err := parseResultRemediations(crclient.scheme, aggregatorConf.ScanName, aggregatorConf.Namespace, contentDom, cm)
		if err != nil {
			fmt.Printf("Cannot parse CM %s into remediations, err: %v\n", cm.Name, err)
		} else if cmParsedResults == nil {
			fmt.Println("Either no parsed results found in result or result already processed")
			continue
		}
		fmt.Printf("CM %s contained %d parsed results\n", cm.Name, len(cmParsedResults))

		prCtx.AddResults(source, cmParsedResults)
		// If the CM was processed, annotate it with the result
		annotateCMWithScanResult(&configMaps[i], cmParsedResults)
	}

	// Once we gathered all results, try to reconcile those that are inconsistent
	consistentParsedResults := prCtx.GetConsistentResults()

	// At this point either scanRemediations is nil or contains a list
	// of remediations for this scan
	// Create the remediations
	fmt.Println("Creating result objects")
	if err := createResults(crclient, scan, consistentParsedResults); err != nil {
		fmt.Printf("Could not create remediation objects: %v\n", err)
		os.Exit(1)
	}

	// Annotate configMaps, so we don't need to re-parse them
	fmt.Println("Annotating ConfigMaps")
	for _, cm := range configMaps {
		err = markConfigMapAsProcessed(crclient, &cm)
		if err != nil {
			fmt.Printf("Cannot annotate the CM: %v\n", err)
			os.Exit(1)
		}
	}
}
