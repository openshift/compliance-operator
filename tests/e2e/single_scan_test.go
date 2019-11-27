package e2e

import (
	goctx "context"
	"fmt"
	"testing"
	"time"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/compliance-operator/pkg/apis"
	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
)

var (
	retryInterval        = time.Second * 5
	timeout              = time.Minute * 10
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Minute * 5
)

func waitForScanDoneStatus(t *testing.T, f *framework.Framework, namespace, name string) error {
	exampleComplianceScan := &complianceoperatorv1alpha1.ComplianceScan{}
	err := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, exampleComplianceScan)
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for availability of %s compliancescan\n", name)
				return false, nil
			} else if apierrors.IsServiceUnavailable(err) {
				t.Logf("The cluster is currently unavailable... Lets keep waiting. Got: %v\n", err)
				return false, nil
			} else if apierrors.IsTimeout(err) {
				t.Logf("The get call timed out... Lets keep waiting. Might be a temporary issue. Got: %v\n", err)
				return false, nil
			}
			return false, err
		}

		if exampleComplianceScan.Status.Phase == complianceoperatorv1alpha1.PhaseDone {
			return true, nil
		}
		t.Logf("Waiting for run of %s compliancescan (%s)\n", name, exampleComplianceScan.Status.Phase)
		return false, nil
	})
	if err != nil {
		return err
	}
	t.Logf("ComplianceScan ready (%s)\n", exampleComplianceScan.Status.Phase)
	return nil
}

func TestSingleScan(t *testing.T) {
	compliancescan := &complianceoperatorv1alpha1.ComplianceScanList{}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, compliancescan)
	if err != nil {
		t.Fatalf("TEST SETUP: failed to add custom resource scheme to framework: %v", err)
	}
	// run subtests
	t.Run("testgroup", func(t *testing.T) {
		t.Run("simple-cluster", ComplianceOperatorCluster)
	})
}

func complianceScanTest(t *testing.T, f *framework.Framework, ctx *framework.TestCtx) error {
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
	return waitForScanDoneStatus(t, f, namespace, "example-scan")
}

func ComplianceOperatorCluster(t *testing.T) {
	t.Parallel()
	ctx := framework.NewTestCtx(t)
	defer ctx.Cleanup()
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

	if err = complianceScanTest(t, f, ctx); err != nil {
		t.Fatal(err)
	}
}
