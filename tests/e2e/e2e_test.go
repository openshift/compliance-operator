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
	ctx := setupTestRequirements(t)
	defer ctx.Cleanup()

	setupComplianceOperatorCluster(t, ctx)

	// get global framework variables
	f := framework.Global

	if err := doSingleComplianceScanTest(t, f, ctx); err != nil {
		t.Fatal(err)
	}
}

func doSingleComplianceScanTest(t *testing.T, f *framework.Framework, ctx *framework.TestCtx) error {
	namespace, err := ctx.GetNamespace()
	if err != nil {
		return fmt.Errorf("could not get namespace: %v", err)
	}
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
	err = f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		return err
	}
	return waitForScanStatus(t, f, namespace, "example-scan", complianceoperatorv1alpha1.PhaseDone)
}

func TestScanWithInvalidContentFails(t *testing.T) {
	t.Skip("NOTE(jaosorior): We're skipping this test until we get CI to use the current built image.")
	ctx := setupTestRequirements(t)
	defer ctx.Cleanup()

	setupComplianceOperatorCluster(t, ctx)

	// get global framework variables
	f := framework.Global

	if err := doScanWithInvalidContentFails(t, f, ctx); err != nil {
		t.Fatal(err)
	}
}

func doScanWithInvalidContentFails(t *testing.T, f *framework.Framework, ctx *framework.TestCtx) error {
	namespace, err := ctx.GetNamespace()
	if err != nil {
		return fmt.Errorf("could not get namespace: %v", err)
	}
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
	err = f.Client.Create(goctx.TODO(), exampleComplianceScan, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		return err
	}
	return waitForScanStatus(t, f, namespace, "example-scan", complianceoperatorv1alpha1.PhaseDone)
}
