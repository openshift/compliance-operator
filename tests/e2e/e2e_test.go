package e2e

import (
	goctx "context"
	"fmt"
	"math/rand"
	"testing"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

func TestE2E(t *testing.T) {
	executeTests(t,
		testExecution{
			Name: "TestSingleScanSucceeds",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				exampleComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-single-scan",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile: "xccdf_org.ssgproject.content_profile_coreos-ncp",
						Content: "ssg-ocp4-ds.xml",
						Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
						Debug:   true,
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-single-scan", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}

				return scanResultIsExpected(f, namespace, "test-single-scan", compv1alpha1.ResultCompliant)
			},
		},
		testExecution{
			Name: "TestSingleTailoredScanSucceeds",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				defaultCleanupOpts := &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval}
				tailoringCM := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-single-tailored-scan-succeeds-cm",
						Namespace: namespace,
					},
					Data: map[string]string{
						"tailoring.xml": `<?xml version="1.0" encoding="UTF-8"?>
<xccdf-1.2:Tailoring xmlns:xccdf-1.2="http://checklists.nist.gov/xccdf/1.2" id="xccdf_compliance.openshift.io_tailoring_test-tailoredprofile">
	<xccdf-1.2:benchmark href="/content/ssg-ocp4-ds.xml"></xccdf-1.2:benchmark>
	<xccdf-1.2:version time="2020-04-28T07:04:13Z">1</xccdf-1.2:version>
	<xccdf-1.2:Profile id="xccdf_compliance.openshift.io_profile_test-tailoredprofile">
		<xccdf-1.2:title>Test Tailored Profile</xccdf-1.2:title>
		<xccdf-1.2:description>Test Tailored Profile</xccdf-1.2:description>
		<xccdf-1.2:select idref="xccdf_org.ssgproject.content_rule_no_netrc_files" selected="true"></xccdf-1.2:select>
	</xccdf-1.2:Profile>
</xccdf-1.2:Tailoring>`,
					},
				}

				err := f.Client.Create(goctx.TODO(), tailoringCM, defaultCleanupOpts)
				if err != nil {
					return err
				}

				exampleComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-single-tailored-scan-succeeds",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile: "xccdf_compliance.openshift.io_profile_test-tailoredprofile",
						Content: "ssg-ocp4-ds.xml",
						Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
						Debug:   true,
						TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
							Name: tailoringCM.Name,
						},
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err = f.Client.Create(goctx.TODO(), exampleComplianceScan, defaultCleanupOpts)
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-single-tailored-scan-succeeds", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}

				return scanResultIsExpected(f, namespace, "test-single-tailored-scan-succeeds", compv1alpha1.ResultCompliant)
			},
		},
		testExecution{
			Name: "TestScanWithNodeSelectorFiltersCorrectly",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				selectWorkers := map[string]string{
					"node-role.kubernetes.io/worker": "",
				}
				testComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-filtered-scan",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile:      "xccdf_org.ssgproject.content_profile_coreos-ncp",
						Content:      "ssg-ocp4-ds.xml",
						Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
						NodeSelector: selectWorkers,
						Debug:        true,
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), testComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-filtered-scan", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}
				nodes := getNodesWithSelector(f, selectWorkers)
				configmaps := getConfigMapsFromScan(f, testComplianceScan)
				if len(nodes) != len(configmaps) {
					return fmt.Errorf(
						"The number of reports doesn't match the number of selected nodes: "+
							"%d reports / %d nodes", len(configmaps), len(nodes))
				}
				return scanResultIsExpected(f, namespace, "test-filtered-scan", compv1alpha1.ResultCompliant)
			},
		},
		testExecution{
			Name: "TestScanWithInvalidContentFails",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				exampleComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-scan-w-invalid-content",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile: "xccdf_org.ssgproject.content_profile_coreos-ncp",
						Content: "ssg-ocp4-non-existent.xml",
						Debug:   true,
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-scan-w-invalid-content", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}
				return scanResultIsExpected(f, namespace, "test-scan-w-invalid-content", compv1alpha1.ResultError)
			},
		},
		testExecution{
			Name: "TestScanWithInvalidProfileFails",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				exampleComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-scan-w-invalid-profile",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile: "xccdf_org.ssgproject.content_profile_coreos-unexistent",
						Content: "ssg-ocp4-ds.xml",
						Debug:   true,
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-scan-w-invalid-profile", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}
				return scanResultIsExpected(f, namespace, "test-scan-w-invalid-profile", compv1alpha1.ResultError)
			},
		},
		testExecution{
			Name: "TestMalformedTailoredScanFails",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				defaultCleanupOpts := &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval}
				tailoringCM := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-malformed-tailored-scan-fails-cm",
						Namespace: namespace,
					},
					// The tailored profile's namespace is wrong. It should be xccdf-1.2, but it was
					// declared as xccdf. So it should report an error
					Data: map[string]string{
						"tailoring.xml": `<?xml version="1.0" encoding="UTF-8"?>
<xccdf-1.2:Tailoring xmlns:xccdf="http://checklists.nist.gov/xccdf/1.2" id="xccdf_compliance.openshift.io_tailoring_test-tailoredprofile">
	<xccdf-1.2:benchmark href="/content/ssg-ocp4-ds.xml"></xccdf-1.2:benchmark>
	<xccdf-1.2:version time="2020-04-28T07:04:13Z">1</xccdf-1.2:version>
	<xccdf-1.2:Profile id="xccdf_compliance.openshift.io_profile_test-tailoredprofile">
		<xccdf-1.2:title>Test Tailored Profile</xccdf-1.2:title>
		<xccdf-1.2:description>Test Tailored Profile</xccdf-1.2:description>
		<xccdf-1.2:select idref="xccdf_org.ssgproject.content_rule_no_netrc_files" selected="true"></xccdf-1.2:select>
	</xccdf-1.2:Profile>
</xccdf-1.2:Tailoring>`,
					},
				}

				err := f.Client.Create(goctx.TODO(), tailoringCM, defaultCleanupOpts)
				if err != nil {
					return err
				}

				exampleComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-malformed-tailored-scan-fails",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile: "xccdf_compliance.openshift.io_profile_test-tailoredprofile",
						Content: "ssg-ocp4-ds.xml",
						Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
						Debug:   true,
						TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
							Name: tailoringCM.Name,
						},
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err = f.Client.Create(goctx.TODO(), exampleComplianceScan, defaultCleanupOpts)
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-malformed-tailored-scan-fails", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}
				return scanResultIsExpected(f, namespace, "test-malformed-tailored-scan-fails", compv1alpha1.ResultError)
			},
		},
		testExecution{
			Name: "TestScanWithEmptyTailoringCMNameFails",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				exampleComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-scan-w-empty-tailoring-cm",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile: "xccdf_org.ssgproject.content_profile_coreos-ncp",
						Content: "ssg-ocp4-ds.xml",
						Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
						TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
							Name: "",
						},
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-scan-w-empty-tailoring-cm", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}
				return scanResultIsExpected(f, namespace, "test-scan-w-empty-tailoring-cm", compv1alpha1.ResultError)
			},
		},
		testExecution{
			Name: "TestScanWithUnexistentTailoringCMFails",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				exampleComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-scan-w-unexistent-tailoring-cm",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile: "xccdf_org.ssgproject.content_profile_coreos-ncp",
						Content: "ssg-ocp4-ds.xml",
						Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
						Debug:   true,
						TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
							Name: "unexistent-tailoring-file",
						},
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-scan-w-unexistent-tailoring-cm", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}
				return scanResultIsExpected(f, namespace, "test-scan-w-unexistent-tailoring-cm", compv1alpha1.ResultError)
			},
		},
		testExecution{
			Name: "TestMissingPodInRunningState",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				exampleComplianceScan := &compv1alpha1.ComplianceScan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-missing-pod-scan",
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceScanSpec{
						Profile: "xccdf_org.ssgproject.content_profile_coreos-ncp",
						Content: "ssg-ocp4-ds.xml",
						Rule:    "xccdf_org.ssgproject.content_rule_no_netrc_files",
						Debug:   true,
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-missing-pod-scan", compv1alpha1.PhaseRunning)
				if err != nil {
					return err
				}
				pods, err := getPodsForScan(f, "test-missing-pod-scan")
				if err != nil {
					return err
				}
				if len(pods) < 1 {
					return fmt.Errorf("No pods gotten from query for the scan")
				}
				podToDelete := pods[rand.Intn(len(pods))]
				// Delete pod ASAP
				zeroSeconds := int64(0)
				do := client.DeleteOptions{GracePeriodSeconds: &zeroSeconds}
				err = f.Client.Delete(goctx.TODO(), &podToDelete, &do)
				if err != nil {
					return err
				}
				err = waitForScanStatus(t, f, namespace, "test-missing-pod-scan", compv1alpha1.PhaseDone)
				if err != nil {
					return err
				}
				return scanResultIsExpected(f, namespace, "test-missing-pod-scan", compv1alpha1.ResultCompliant)
			},
		},
		testExecution{
			Name: "TestApplyGenericRemediation",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				remName := "test-apply-generic-remediation"
				genericRem := &compv1alpha1.ComplianceRemediation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      remName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceRemediationSpec{
						ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
							Apply: true,
						},
						Object: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "ConfigMap",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name":      "generic-rem-cm",
									"namespace": namespace,
								},
								"data": map[string]interface{}{
									"key": "value",
								},
							},
						},
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), genericRem, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForRemediationState(t, f, namespace, remName, compv1alpha1.RemediationApplied)
				if err != nil {
					return err
				}

				cm := &corev1.ConfigMap{}
				err = waitForObjectToExist(t, f, "generic-rem-cm", namespace, cm)
				if err != nil {
					return err
				}
				val, ok := cm.Data["key"]
				if !ok || val != "value" {
					return fmt.Errorf("ComplianceRemediation '%s' generated a malformed ConfigMap", remName)
				}
				return nil
			},
		},
		testExecution{
			Name: "TestGenericRemediationFailsWithUnkownType",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				remName := "test-generic-remediation-fails-unkown"
				genericRem := &compv1alpha1.ComplianceRemediation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      remName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceRemediationSpec{
						ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
							Apply: true,
						},
						Object: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "OopsyDoodle",
								"apiVersion": "foo.bar/v1",
								"metadata": map[string]interface{}{
									"name":      "unkown-remediation",
									"namespace": namespace,
								},
								"data": map[string]interface{}{
									"key": "value",
								},
							},
						},
					},
				}
				// use Context's create helper to create the object and add a cleanup function for the new object
				err := f.Client.Create(goctx.TODO(), genericRem, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				err = waitForRemediationState(t, f, namespace, remName, compv1alpha1.RemediationError)
				if err != nil {
					return err
				}
				return nil
			},
		},
		testExecution{
			Name: "TestSuiteScan",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				suiteName := "test-suite-two-scans"

				workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)
				selectWorkers := map[string]string{
					"node-role.kubernetes.io/worker": "",
				}

				masterScanName := fmt.Sprintf("%s-masters-scan", suiteName)
				selectMasters := map[string]string{
					"node-role.kubernetes.io/master": "",
				}

				exampleComplianceSuite := &compv1alpha1.ComplianceSuite{
					ObjectMeta: metav1.ObjectMeta{
						Name:      suiteName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceSuiteSpec{
						AutoApplyRemediations: false,
						Scans: []compv1alpha1.ComplianceScanSpecWrapper{
							{
								ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
									ContentImage: "quay.io/jhrozek/ocp4-openscap-content:ignition_remediation",
									Profile:      "xccdf_org.ssgproject.content_profile_coreos-ncp",
									Content:      "ssg-ocp4-ds.xml",
									NodeSelector: selectWorkers,
									Debug:        true,
								},
								Name: workerScanName,
							},
							{
								ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
									ContentImage: "quay.io/jhrozek/ocp4-openscap-content:ignition_remediation",
									Profile:      "xccdf_org.ssgproject.content_profile_coreos-ncp",
									Content:      "ssg-ocp4-ds.xml",
									NodeSelector: selectMasters,
									Debug:        true,
								},
								Name: masterScanName,
							},
						},
					},
				}

				err := f.Client.Create(goctx.TODO(), exampleComplianceSuite, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}

				// Ensure that all the scans in the suite have finished and are marked as Done
				err = waitForSuiteScansStatus(t, f, namespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
				if err != nil {
					return err
				}

				// At this point, both scans should be non-compliant given our current content
				err = scanResultIsExpected(f, namespace, workerScanName, compv1alpha1.ResultNonCompliant)
				if err != nil {
					return err
				}
				err = scanResultIsExpected(f, namespace, masterScanName, compv1alpha1.ResultNonCompliant)
				if err != nil {
					return err
				}

				// Each scan should produce two remediations
				workerRemediations := []string{
					fmt.Sprintf("%s-no-empty-passwords", workerScanName),
					fmt.Sprintf("%s-no-direct-root-logins", workerScanName),
				}
				err = assertHasRemediations(t, f, suiteName, workerScanName, "worker", workerRemediations)
				if err != nil {
					return err
				}

				masterRemediations := []string{
					fmt.Sprintf("%s-no-empty-passwords", masterScanName),
					fmt.Sprintf("%s-no-direct-root-logins", masterScanName),
				}
				err = assertHasRemediations(t, f, suiteName, masterScanName, "master", masterRemediations)
				if err != nil {
					return err
				}

				checkWifiInBios := compv1alpha1.ComplianceCheckResult{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-wireless-disable-in-bios", workerScanName),
						Namespace: namespace,
					},
					ID:     "xccdf_org.ssgproject.content_rule_wireless_disable_in_bios",
					Status: compv1alpha1.CheckResultInfo,
				}

				err = assertHasCheck(f, suiteName, workerScanName, checkWifiInBios)
				if err != nil {
					return err
				}

				return nil
			},
		},
		testExecution{
			Name: "TestAutoRemediate",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				// FIXME, maybe have a func that returns a struct with suite name and scan names?
				suiteName := "test-remediate"
				workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)

				exampleComplianceSuite := &compv1alpha1.ComplianceSuite{
					ObjectMeta: metav1.ObjectMeta{
						Name:      suiteName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceSuiteSpec{
						AutoApplyRemediations: true,
						Scans: []compv1alpha1.ComplianceScanSpecWrapper{
							{
								ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
									ContentImage: "quay.io/jhrozek/ocp4-openscap-content:ignition_remediation",
									Profile:      "xccdf_org.ssgproject.content_profile_coreos-ncp",
									Rule:         "xccdf_org.ssgproject.content_rule_no_direct_root_logins",
									Content:      "ssg-ocp4-ds.xml",
									NodeSelector: getPoolNodeRoleSelector(),
									Debug:        true,
								},
								Name: workerScanName,
							},
						},
					},
				}

				err := mcTctx.createE2EPool()
				if err != nil {
					t.Errorf("Cannot create subpool for this test")
					return err
				}

				err = f.Client.Create(goctx.TODO(), exampleComplianceSuite, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}

				// Get the MachineConfigPool before a scan or remediation has been applied
				// This way, we can check that it changed without race-conditions
				poolBeforeRemediation := &mcfgv1.MachineConfigPool{}
				err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: testPoolName}, poolBeforeRemediation)
				if err != nil {
					return err
				}

				// Ensure that all the scans in the suite have finished and are marked as Done
				err = waitForSuiteScansStatus(t, f, namespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
				if err != nil {
					return err
				}

				// We need to check that the remediation is auto-applied and save
				// the object so we can delete it later
				workersNoRootLoginsRemName := fmt.Sprintf("%s-no-direct-root-logins", workerScanName)
				err = waitForRemediationToBeAutoApplied(t, f, workersNoRootLoginsRemName, namespace, poolBeforeRemediation)
				if err != nil {
					t.Errorf("Failed to wait for nodes to come back up after applying MC: %v", err)
					return err
				}

				// We can re-run the scan at this moment and check that we got one less remediation
				secondSuiteName := "test-recheck-remediations"
				secondWorkerScanName := fmt.Sprintf("%s-workers-scan", secondSuiteName)

				secondSuite := &compv1alpha1.ComplianceSuite{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secondSuiteName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceSuiteSpec{
						AutoApplyRemediations: false,
						Scans: []compv1alpha1.ComplianceScanSpecWrapper{
							{
								ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
									ContentImage: "quay.io/jhrozek/ocp4-openscap-content:ignition_remediation",
									Profile:      "xccdf_org.ssgproject.content_profile_coreos-ncp",
									Rule:         "xccdf_org.ssgproject.content_rule_no_direct_root_logins",
									Content:      "ssg-ocp4-ds.xml",
									NodeSelector: getPoolNodeRoleSelector(),
									Debug:        true,
								},
								Name: secondWorkerScanName,
							},
						},
					},
				}

				err = f.Client.Create(goctx.TODO(), secondSuite, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}
				t.Logf("Second scan launched")

				// Ensure that all the scans in the suite have finished and are marked as Done
				err = waitForSuiteScansStatus(t, f, namespace, secondSuiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
				if err != nil {
					return err
				}
				t.Logf("Second scan finished")

				// Now the remediation should not be created
				workersNoRootLoginsRemName2 := fmt.Sprintf("%s-no-direct-root-logins", secondWorkerScanName)
				remCheck := &compv1alpha1.ComplianceRemediation{}
				err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: workersNoRootLoginsRemName2, Namespace: namespace}, remCheck)
				if err == nil {
					return fmt.Errorf("remediation %s found unexpectedly", workersNoRootLoginsRemName2)
				} else if !errors.IsNotFound(err) {
					t.Errorf("Unexpected error %v", err)
					return err
				}

				// The test should not leave junk around, let's remove the MC and wait for the nodes to stabilize
				// again
				t.Logf("Removing applied remediation")
				// Fetch remediation here so it can be deleted
				rem := &compv1alpha1.ComplianceRemediation{}
				err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: workersNoRootLoginsRemName, Namespace: namespace}, rem)
				if err != nil {
					return err
				}
				mcfgToBeDeleted := rem.Spec.Object.DeepCopy()
				mcfgToBeDeleted.SetName(rem.GetMcName())
				err = f.Client.Delete(goctx.TODO(), mcfgToBeDeleted)
				if err != nil {
					return err
				}

				t.Logf("MC deleted, will wait for the machines to come back up")

				dummyAction := func() error {
					return nil
				}
				poolHasNoMc := func(t *testing.T, pool *mcfgv1.MachineConfigPool) (bool, error) {
					for _, mc := range pool.Status.Configuration.Source {
						if mc.Name == rem.GetMcName() {
							return false, nil
						}
					}

					return true, nil
				}

				// We need to wait for both the pool to update..
				err = waitForMachinePoolUpdate(t, f, testPoolName, dummyAction, poolHasNoMc, nil)
				if err != nil {
					t.Errorf("Failed to wait for workers to come back up after deleting MC")
					return err
				}

				// ..as well as the nodes
				err = waitForNodesToBeReady(t, f)
				if err != nil {
					t.Errorf("Failed to wait for nodes to come back up after applying MC: %v", err)
					return err
				}

				t.Logf("The test succeeded!")
				return nil
			},
		},
		testExecution{
			Name: "TestUnapplyRemediation",
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
				// FIXME, maybe have a func that returns a struct with suite name and scan names?
				suiteName := "test-unapply-remediation"

				workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)

				exampleComplianceSuite := &compv1alpha1.ComplianceSuite{
					ObjectMeta: metav1.ObjectMeta{
						Name:      suiteName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.ComplianceSuiteSpec{
						AutoApplyRemediations: false,
						Scans: []compv1alpha1.ComplianceScanSpecWrapper{
							{
								ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
									ContentImage: "quay.io/jhrozek/ocp4-openscap-content:ignition_remediation",
									Profile:      "xccdf_org.ssgproject.content_profile_coreos-ncp",
									Content:      "ssg-ocp4-ds.xml",
									NodeSelector: getPoolNodeRoleSelector(),
									Debug:        true,
								},
								Name: workerScanName,
							},
						},
					},
				}

				err := mcTctx.createE2EPool()
				if err != nil {
					t.Errorf("Cannot create subpool for this test")
					return err
				}

				err = f.Client.Create(goctx.TODO(), exampleComplianceSuite, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
				if err != nil {
					return err
				}

				// Ensure that all the scans in the suite have finished and are marked as Done
				err = waitForSuiteScansStatus(t, f, namespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
				if err != nil {
					return err
				}

				// Pause the MC so that we have only one reboot
				err = pauseMachinePool(t, f, testPoolName)
				if err != nil {
					return err
				}

				// Apply both remediations
				workersNoRootLoginsRemName := fmt.Sprintf("%s-no-direct-root-logins", workerScanName)
				err = applyRemediationAndCheck(t, f, namespace, workersNoRootLoginsRemName, testPoolName)
				if err != nil {
					t.Logf("WARNING: Got an error while applying remediation '%s': %v", workersNoRootLoginsRemName, err)
				}
				t.Logf("Remediation %s applied", workersNoRootLoginsRemName)

				workersNoEmptyPassRemName := fmt.Sprintf("%s-no-empty-passwords", workerScanName)
				err = applyRemediationAndCheck(t, f, namespace, workersNoEmptyPassRemName, testPoolName)
				if err != nil {
					t.Logf("WARNING: Got an error while applying remediation '%s': %v", workersNoEmptyPassRemName, err)
				}
				t.Logf("Remediation %s applied", workersNoEmptyPassRemName)

				// unpause the MCP so that the remediation gets applied
				err = unPauseMachinePoolAndWait(t, f, testPoolName)
				if err != nil {
					return err
				}

				err = waitForNodesToBeReady(t, f)
				if err != nil {
					t.Errorf("Failed to wait for nodes to come back up after applying MC: %v", err)
					return err
				}

				// Get the resulting MC
				mcName := types.NamespacedName{Name: fmt.Sprintf("75-%s-%s", workerScanName, suiteName)}
				mcBoth := &mcfgv1.MachineConfig{}
				err = f.Client.Get(goctx.TODO(), mcName, mcBoth)
				t.Logf("MC %s exists", mcName.Name)

				// Revert one remediation. The MC should stay, but its generation should bump
				t.Logf("Will revert remediation %s", workersNoEmptyPassRemName)
				err = unApplyRemediationAndCheck(t, f, namespace, workersNoEmptyPassRemName, testPoolName, false)
				if err != nil {
					t.Logf("WARNING: Got an error while unapplying remediation '%s': %v", workersNoEmptyPassRemName, err)
				}
				t.Logf("Remediation %s reverted", workersNoEmptyPassRemName)
				mcOne := &mcfgv1.MachineConfig{}
				err = f.Client.Get(goctx.TODO(), mcName, mcOne)

				if mcOne.Generation == mcBoth.Generation {
					t.Errorf("Expected that the MC generation changes. Got: %d, Expected: %d", mcOne.Generation, mcBoth.Generation)
				}

				// When we unapply the second remediation, the MC should be deleted, too
				t.Logf("Will revert remediation %s", workersNoRootLoginsRemName)
				err = unApplyRemediationAndCheck(t, f, namespace, workersNoRootLoginsRemName, testPoolName, true)
				t.Logf("Remediation %s reverted", workersNoEmptyPassRemName)

				t.Logf("No remediation-based MCs should exist now")
				mcShouldntExist := &mcfgv1.MachineConfig{}
				err = f.Client.Get(goctx.TODO(), mcName, mcShouldntExist)
				if err == nil {
					t.Errorf("MC %s unexpectedly found", mcName)
				}

				return nil
			},
		},
	)
}
