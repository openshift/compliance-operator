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

	"github.com/antchfx/xmlquery"
	backoff "github.com/cenkalti/backoff/v4"
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
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
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

	logf.SetLogger(zap.Logger())

	return &conf
}

func getScanConfigMaps(crClient *complianceCrClient, scan, namespace string) ([]v1.ConfigMap, error) {
	cMapList := &v1.ConfigMapList{}
	var err error

	// Look for configMap with this scan label
	inNs := client.InNamespace(namespace)
	withLabel := client.MatchingLabels{
		compv1alpha1.ComplianceScanLabel: scan,
		compv1alpha1.ResultLabel:         "",
	}

	err = crClient.client.List(context.TODO(), cMapList, inNs, withLabel)
	if err != nil {
		log.Error(err, "Error waiting for CMs of scan", "ComplianceScan.Name", scan)
		return nil, err
	}

	if len(cMapList.Items) == 0 {
		log.Info("Scan has no results", "ComplianceScan.Name", scan)
		return make([]v1.ConfigMap, 0), nil
	}

	log.Info("Scan has results", "ComplianceScan.Name", scan, "results-length", len(cMapList.Items))
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
func parseResultRemediations(scheme *runtime.Scheme, scanName, namespace string, content *xmlquery.Node, cm *v1.ConfigMap) ([]*utils.ParseResult, string, error) {
	var scanReader io.Reader

	_, ok := cm.Annotations[configMapRemediationsProcessed]
	if ok {
		log.Info("ConfigMap already processed", "ConfigMap.Name", cm.Name)
		return nil, "", nil
	}

	cmScanResult, ok := cm.Data["results"]
	if !ok {
		return nil, "", fmt.Errorf("no results in configmap %s", cm.Name)
	}

	_, ok = cm.Annotations[configMapCompressed]
	if ok {
		log.Info("Results are compressed\n")
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
		case common.OpenSCAPExitCodeCompliant:
			return compv1alpha1.ResultCompliant, ""
		case common.OpenSCAPExitCodeNonCompliant:
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
		log.Error(err, "Failed to set ownership", "kind", kind.GroupVersionKind().Kind)
		return err
	}

	res.SetLabels(labels)
	if annotations != nil {
		res.SetAnnotations(annotations)
	}

	name := res.GetName()

	err := backoff.Retry(func() error {
		var err error
		if !exists {
			log.Info("Creating object", "kind", kind, "name", name)
			err = crClient.client.Create(context.TODO(), res)
		} else {
			log.Info("Updating object", "kind", kind, "name", name)
			err = crClient.client.Update(context.TODO(), res)
		}
		if err != nil && !errors.IsAlreadyExists(err) {
			log.Error(err, "Retrying with a backoff because of an error while creating or updating object")
			return err
		}
		return nil
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
	if err != nil {
		log.Error(err, "Failed to create an object", "kind", kind.GroupVersionKind().Kind)
		return err
	}
	return nil
}

func canCreateRemediation(scan *compv1alpha1.ComplianceScan, obj runtime.Object) (bool, string) {
	// FIXME(jaosorior): Figure out a more pluggable way of adding these sorts of special cases
	if obj.GetObjectKind().GroupVersionKind().Kind != "MachineConfig" {
		return true, ""
	}

	role := utils.GetFirstNodeRole(scan.Spec.NodeSelector)
	if role == "" {
		return false, "ComplianceScan's nodeSelector doesn't have any role. Ideally this needs to match a MachineConfigPool"
	}
	return true, ""
}

func getRemediationLabels(scan *compv1alpha1.ComplianceScan, obj runtime.Object) map[string]string {
	labels := make(map[string]string)
	labels[compv1alpha1.ComplianceScanLabel] = scan.Name
	labels[compv1alpha1.SuiteLabel] = scan.Labels[compv1alpha1.SuiteLabel]

	return labels
}

func getCheckResultLabels(pr *utils.ParseResult, resultLabels map[string]string, scan *compv1alpha1.ComplianceScan) map[string]string {
	labels := make(map[string]string)
	labels[compv1alpha1.ComplianceScanLabel] = scan.Name
	labels[compv1alpha1.SuiteLabel] = scan.Labels[compv1alpha1.SuiteLabel]
	labels[compv1alpha1.ComplianceCheckResultStatusLabel] = string(pr.CheckResult.Status)
	labels[compv1alpha1.ComplianceCheckResultSeverityLabel] = string(pr.CheckResult.Severity)
	if pr.Remediation != nil {
		labels[compv1alpha1.ComplianceCheckResultHasRemediation] = ""
	}

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
	log.Info("Will create result objects", "objects", len(consistentResults))
	if len(consistentResults) == 0 {
		log.Info("Nothing to create")
		return nil
	}

	var remediationNotPossibleEventIssued bool

	for _, pr := range consistentResults {
		if pr == nil || pr.CheckResult == nil {
			log.Info("nil result or result.check, this shouldn't happen")
			continue
		}

		checkResultLabels := getCheckResultLabels(&pr.ParseResult, pr.Labels, scan)
		checkResultAnnotations := getCheckResultAnnotations(pr.CheckResult, pr.Annotations)

		crkey := getObjKey(pr.CheckResult.GetName(), pr.CheckResult.GetNamespace())
		foundCheckResult := &compv1alpha1.ComplianceCheckResult{}
		// Copy type metadata so dynamic client copies data correctly
		foundCheckResult.TypeMeta = pr.CheckResult.TypeMeta
		log.Info("Getting ComplianceCheckResult", "ComplianceCheckResult.Name", crkey.Name,
			"ComplianceCheckResult.Namespace", crkey.Namespace)
		checkResultExists := getObjectIfFound(crClient, crkey, foundCheckResult)
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
				pr.CheckResult.Status != compv1alpha1.CheckResultPass && /* even passing remediations might need to be updated */
				pr.CheckResult.Status != compv1alpha1.CheckResultInconsistent) {
			continue
		}

		remTargetObj := pr.Remediation.Spec.Current.Object

		if canCreate, why := canCreateRemediation(scan, remTargetObj); !canCreate {
			// Only issue event once.
			if !remediationNotPossibleEventIssued {
				log.Info(why)
				crClient.recorder.Event(scan, v1.EventTypeWarning, "CannotRemediate", why)
				remediationNotPossibleEventIssued = true
			}
			continue
		}

		remLabels := getRemediationLabels(scan, remTargetObj)

		// The state even if set in the object would have been overwritten by the call to
		// spec update, so we keep the state separately in a variable
		stateUpdate := compv1alpha1.RemediationPending

		remkey := getObjKey(pr.Remediation.GetName(), pr.Remediation.GetNamespace())
		foundRemediation := &compv1alpha1.ComplianceRemediation{}
		// Copy type metadata so dynamic client copies data correctly
		foundRemediation.TypeMeta = pr.Remediation.TypeMeta
		log.Info("Getting ComplianceRemediation", "ComplianceRemediation.Name", crkey.Name,
			"ComplianceRemediation.Namespace", crkey.Namespace)
		remExists := getObjectIfFound(crClient, remkey, foundRemediation)
		if remExists {
			// If the remediation is already applied and the status of the check is compliant, only update
			// the remediation if the payload differs. Let's not create remediations for checks that are passing
			// needlessly and let's not trigger the remediation controller needlessly
			if foundRemediation.Status.ApplicationState == compv1alpha1.RemediationApplied ||
				foundRemediation.Status.ApplicationState == compv1alpha1.RemediationOutdated {
				if !foundRemediation.RemediationPayloadDiffers(pr.Remediation) {
					log.Info("Not updating passing remediation that was the same between runs", "ComplianceRemediation.Name", foundRemediation.Name)
					continue
				}

				// Applied remediation that differs must be updated, let's set the appropriate state
				stateUpdate = compv1alpha1.RemediationOutdated
				if foundRemediation.Status.ApplicationState == compv1alpha1.RemediationApplied {
					// For applied remediations, the old state must be kept in the outdated field
					// so that the admin can switch to the current state at their own pace
					foundRemediation.Spec.Current.DeepCopyInto(&pr.Remediation.Spec.Outdated)
				} else {
					// For remediations that were already outdated, keep the outdated data there
					foundRemediation.Spec.Outdated.DeepCopyInto(&pr.Remediation.Spec.Outdated)
				}
				// The application state must be preserved or else we'd un-apply the remediation
				// once it's updated
				pr.Remediation.Spec.Apply = foundRemediation.Spec.Apply
				// Also label the outdated remediations so that the admin can find them
				remLabels[compv1alpha1.OutdatedRemediationLabel] = ""
			}

			// Copy resource version and other metadata needed for update
			foundRemediation.ObjectMeta.DeepCopyInto(&pr.Remediation.ObjectMeta)
		} else if pr.CheckResult.Status == compv1alpha1.CheckResultPass {
			// If the remediation was not created earlier (e.g. the check was always passing), don't bother
			// creating it now
			continue
		}

		// remediation is owned by the check
		if err := createOrUpdateOneResult(crClient, pr.CheckResult, remLabels, nil, remExists, pr.Remediation); err != nil {
			return fmt.Errorf("cannot create or update remediation %s: %v", pr.Remediation.Name, err)
		}

		// Update the status as needed
		if remExists {
			if err := updateRemediationStatus(crClient, pr.Remediation, stateUpdate); err != nil {
				return err
			}
		}
	}

	return nil
}

func updateRemediationStatus(crClient *complianceCrClient, parsedRemediation *compv1alpha1.ComplianceRemediation, state compv1alpha1.RemediationApplicationState) error {
	remkey := getObjKey(parsedRemediation.GetName(), parsedRemediation.GetNamespace())
	foundRemediation := &compv1alpha1.ComplianceRemediation{}
	// Copy type metadata so dynamic client copies data correctly
	log.Info("Updating remediation status", "ComplianceRemediation.Name", remkey.Name,
		"ComplianceRemediation.Namespace", remkey.Namespace)

	return backoff.Retry(func() error {
		if err := crClient.client.Get(context.TODO(), remkey, foundRemediation); err != nil {
			return fmt.Errorf("cannot update remediation status %s: %v", parsedRemediation.Name, err)

		}
		foundRemediation.Status.ErrorMessage = ""
		foundRemediation.Status.ApplicationState = state
		err := crClient.client.Status().Update(context.TODO(), foundRemediation)
		if err != nil {
			return fmt.Errorf("cannot update remediation status %s: %v", parsedRemediation.Name, err)
		}
		return nil
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))
}

func getObjKey(name, ns string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: ns}
}

// Returns whether or not an object exists, and updates the data in the obj.
func getObjectIfFound(crClient *complianceCrClient, key types.NamespacedName, obj runtime.Object) bool {
	var found bool
	err := backoff.Retry(func() error {
		err := crClient.client.Get(context.TODO(), key, obj)
		if errors.IsNotFound(err) {
			return nil
		} else if err != nil {
			log.Error(err, "Retrying with a backoff because of an error while getting object")
			return err
		}
		found = true
		return nil
	}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))

	if err != nil {
		log.Error(err, "Couldn't get object", "Name", key.Name, "Namespace", key.Namespace)
	}
	return found
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
		log.Error(err, "Cannot create kube client for compliance-operator types")
		os.Exit(1)
	}

	err = crclient.useEventRecorder("aggregator", cfg)
	if err != nil {
		log.Error(err, "Cannot create event recorder")
		os.Exit(1)
	}

	var scan = &compv1alpha1.ComplianceScan{}
	err = crclient.client.Get(context.TODO(), types.NamespacedName{
		Namespace: aggregatorConf.Namespace,
		Name:      aggregatorConf.ScanName,
	}, scan)
	if err != nil {
		log.Error(err, "Cannot retrieve the scan instance",
			"ComplianceScan.Name", aggregatorConf.ScanName,
			"ComplianceScan.Namespace", aggregatorConf.Namespace,
		)
		os.Exit(1)
	}

	// Find all the configmaps for a scan
	configMaps, err := getScanConfigMaps(crclient, aggregatorConf.ScanName, common.GetComplianceOperatorNamespace())
	if err != nil {
		log.Error(err, "getScanConfigMaps failed")
		os.Exit(1)
	}

	contentFile, err := readContent(aggregatorConf.Content)
	if err != nil {
		log.Error(err, "Cannot read the content")
		os.Exit(1)
	}
	// #nosec
	defer contentFile.Close()
	bufContentFile := bufio.NewReader(contentFile)
	contentDom, err := utils.ParseContent(bufContentFile)
	if err != nil {
		log.Error(err, "Cannot parse the content")
		os.Exit(1)
	}

	prCtx := utils.NewParseResultContext()

	// For each configmap, create a list of remediations
	for i := range configMaps {
		cm := &configMaps[i]
		log.Info("processing ConfigMap", "ConfigMap.Name", cm.Name)

		cmParsedResults, source, err := parseResultRemediations(crclient.scheme, aggregatorConf.ScanName, aggregatorConf.Namespace, contentDom, cm)
		if err != nil {
			log.Error(err, "Cannot parse ConfigMap into remediations", "ConfigMap.Name", cm.Name)
		} else if cmParsedResults == nil {
			log.Info("Either no parsed results found in result or result already processed")
			continue
		}
		log.Info("ConfigMap contained parsed results", "ConfigMap.Name", cm.Name, "results", len(cmParsedResults))

		prCtx.AddResults(source, cmParsedResults)
		// If the CM was processed, annotate it with the result
		annotateCMWithScanResult(&configMaps[i], cmParsedResults)
	}

	// Once we gathered all results, try to reconcile those that are inconsistent
	consistentParsedResults := prCtx.GetConsistentResults()

	// At this point either scanRemediations is nil or contains a list
	// of remediations for this scan
	// Create the remediations
	log.Info("Creating result objects")
	if err := createResults(crclient, scan, consistentParsedResults); err != nil {
		log.Error(err, "Could not create remediation objects")
		os.Exit(1)
	}

	// Annotate configMaps, so we don't need to re-parse them
	log.Info("Annotating ConfigMaps")
	for idx := range configMaps {
		err = markConfigMapAsProcessed(crclient, &configMaps[idx])
		if err != nil {
			log.Error(err, "Cannot annotate the ConfigMap")
			os.Exit(1)
		}
	}
}
