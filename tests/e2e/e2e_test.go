package e2e

import (
	goctx "context"
	"fmt"
	"testing"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
)

func TestSingleScanSucceeds(t *testing.T) {
	executeTest(t, func(t *testing.T, f *framework.Framework, ctx *framework.TestCtx, namespace string) error {
		exampleComplianceScan := &complianceoperatorv1alpha1.ComplianceScan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "example-scan",
				Namespace: namespace,
			},
			Spec: complianceoperatorv1alpha1.ComplianceScanSpec{
				Profile: "xccdf_org.ssgproject.content_profile_coreos-ncp",
				Content: "ssg-ocp4-ds.xml",
			},
		}
		// use TestCtx's create helper to create the object and add a cleanup function for the new object
		err := f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
		if err != nil {
			return err
		}
		return waitForScanStatus(t, f, namespace, "example-scan", complianceoperatorv1alpha1.PhaseDone)
	})
}

func TestScanWithNodeSelectorFiltersCorrectly(t *testing.T) {
	executeTest(t, func(t *testing.T, f *framework.Framework, ctx *framework.TestCtx, namespace string) error {
		selectWorkers := map[string]string{
			"node-role.kubernetes.io/worker": "",
		}
		testComplianceScan := &complianceoperatorv1alpha1.ComplianceScan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-filtered-scan",
				Namespace: namespace,
			},
			Spec: complianceoperatorv1alpha1.ComplianceScanSpec{
				Profile:      "xccdf_org.ssgproject.content_profile_coreos-ncp",
				Content:      "ssg-ocp4-ds.xml",
				NodeSelector: selectWorkers,
			},
		}
		// use TestCtx's create helper to create the object and add a cleanup function for the new object
		err := f.Client.Create(goctx.TODO(), testComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
		if err != nil {
			return err
		}
		err = waitForScanStatus(t, f, namespace, "test-filtered-scan", complianceoperatorv1alpha1.PhaseDone)
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
		return nil
	})
}

func TestScanWithInvalidContentFails(t *testing.T) {
	executeTest(t, func(t *testing.T, f *framework.Framework, ctx *framework.TestCtx, namespace string) error {
		exampleComplianceScan := &complianceoperatorv1alpha1.ComplianceScan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "example-scan",
				Namespace: namespace,
			},
			Spec: complianceoperatorv1alpha1.ComplianceScanSpec{
				Profile: "xccdf_org.ssgproject.content_profile_coreos-ncp",
				Content: "ssg-ocp4-non-existent.xml",
			},
		}
		// use TestCtx's create helper to create the object and add a cleanup function for the new object
		err := f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
		if err != nil {
			return err
		}
		return waitForScanStatus(t, f, namespace, "example-scan", complianceoperatorv1alpha1.PhaseDone)
	})
}
