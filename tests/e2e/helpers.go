package e2e

import (
	goctx "context"
	"testing"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/compliance-operator/pkg/apis"
	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
)

func setupTestRequirements(t *testing.T) *framework.TestCtx {
	compliancescan := &complianceoperatorv1alpha1.ComplianceScanList{}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, compliancescan)
	if err != nil {
		t.Fatalf("TEST SETUP: failed to add custom resource scheme to framework: %v", err)
	}
	return framework.NewTestCtx(t)
}

func setupComplianceOperatorCluster(t *testing.T, ctx *framework.TestCtx) {
	err := ctx.InitializeClusterResources(&framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		t.Fatalf("failed to initialize cluster resources: %v", err)
	}
	t.Log("Initialized cluster resources")
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatal(err)
	}
	// get global framework variables
	f := framework.Global
	// wait for compliance-operator to be ready
	err = e2eutil.WaitForOperatorDeployment(t, f.KubeClient, namespace, "compliance-operator", 1, retryInterval, timeout)
	if err != nil {
		t.Fatal(err)
	}
}

func waitForScanStatus(t *testing.T, f *framework.Framework, namespace, name string, targetStaus complianceoperatorv1alpha1.ComplianceScanStatusPhase) error {
	exampleComplianceScan := &complianceoperatorv1alpha1.ComplianceScan{}
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(retryInterval, timeout, func() (bool, error) {
		lastErr = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, exampleComplianceScan)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				t.Logf("Waiting for availability of %s compliancescan\n", name)
				return false, nil
			}
			t.Logf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		if exampleComplianceScan.Status.Phase == targetStaus {
			return true, nil
		}
		t.Logf("Waiting for run of %s compliancescan (%s)\n", name, exampleComplianceScan.Status.Phase)
		return false, nil
	})
	// Error in function call
	if lastErr != nil {
		return lastErr
	}
	// Timeout
	if timeouterr != nil {
		return timeouterr
	}
	t.Logf("ComplianceScan ready (%s)\n", exampleComplianceScan.Status.Phase)
	return nil
}
