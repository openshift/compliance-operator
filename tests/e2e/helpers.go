package e2e

import (
	goctx "context"
	"errors"
	"fmt"
	"github.com/openshift/compliance-operator/pkg/utils"
	"testing"
	"time"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/compliance-operator/pkg/apis"
	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
	mcfgv1 "github.com/openshift/compliance-operator/pkg/apis/machineconfiguration/v1"
	mcfgClient "github.com/openshift/compliance-operator/pkg/generated/clientset/versioned/typed/machineconfiguration/v1"
)

const (
	workerPoolName = "worker"
	testPoolName   = "e2e"
)

type testExecution struct {
	Name   string
	TestFn func(*testing.T, *framework.Framework, *framework.TestCtx, *mcTestCtx, string) error
}

type mcTestCtx struct {
	mcClient *mcfgClient.MachineconfigurationV1Client

	f     *framework.Framework
	t     *testing.T
	pools []*mcfgv1.MachineConfigPool
}

func NewMcTestCtx(f *framework.Framework, t *testing.T) (*mcTestCtx, error) {
	mcClient, err := mcfgClient.NewForConfig(f.KubeConfig)
	if err != nil {
		return nil, err
	}

	return &mcTestCtx{mcClient: mcClient, f: f, t: t}, nil
}

func (c *mcTestCtx) cleanupTrackedPools() {
	for _, p := range c.pools {
		// Then find all nodes that are labeled with this pool and remove the label
		// Search the nodes with this label
		poolNodes := getNodesWithSelector(c.f, p.Spec.NodeSelector.MatchLabels)
		rmPoolLabel := utils.GetFirstNodeRoleLabel(p.Spec.NodeSelector.MatchLabels)

		err := unLabelNodes(c.t, c.f, rmPoolLabel, poolNodes)
		if err != nil {
			c.t.Errorf("Could not unlabel nodes from pool %s: %v\n", rmPoolLabel, err)
		}

		// Unlabeling the nodes triggers an update of the affected nodes because the nodes
		// will then start using a different rendered pool. e.g a node that used to be labeled
		// with "e2e,worker" and becomes labeled with "worker" switches from "rendered-e2e-*"
		// to "rendered-worker-*". If we didn't wait, the node might have tried to use the
		// e2e pool that would be gone when we remove it with the next call
		err = waitForNodesToHaveARenderedPool(c.t, c.f, c.mcClient, poolNodes, workerPoolName)
		if err != nil {
			c.t.Errorf("Error waiting for nodes to reach the worker pool again: %v\n", err)
		}

		err = waitForPoolCondition(c.t, c.mcClient, mcfgv1.MachineConfigPoolUpdated, p.Name)
		if err != nil {
			c.t.Errorf("Error waiting for reboot after nodes were unlabeled: %v\n", err)
		}

		// Then delete the pool itself
		c.t.Logf("Removing pool %s\n", p.Name)
		err = c.mcClient.MachineConfigPools().Delete(p.Name, &metav1.DeleteOptions{})
		if err != nil {
			c.t.Errorf("Could not remove pool %s: %v\n", p.Name, err)
		}
	}
}

func (c *mcTestCtx) trackPool(pool *mcfgv1.MachineConfigPool) {
	for _, p := range c.pools {
		if p.Name == pool.Name {
			return
		}
	}
	c.pools = append(c.pools, pool)
	c.t.Logf("Tracking pool %s\n", pool.Name)
}

func (c *mcTestCtx) createE2EPool() error {
	pool, err := createReadyMachineConfigPoolSubset(c.t, c.f, c.mcClient, workerPoolName, testPoolName)
	if apierrors.IsAlreadyExists(err) {
		return nil
	} else if err != nil {
		return err
	}
	c.trackPool(pool)
	return nil
}

// executeTest sets up everything that a e2e test needs to run, and executes the test.
func executeTests(t *testing.T, tests ...testExecution) {
	ctx := setupTestRequirements(t)
	defer cleanupTestEnv(t, ctx)

	setupComplianceOperatorCluster(t, ctx)

	// get global framework variables
	f := framework.Global

	ns, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("could not get namespace: %v", err)
	}

	mcTctx, err := NewMcTestCtx(f, t)
	if err != nil {
		t.Fatalf("could not create the MC test context: %v", err)
	}
	defer mcTctx.cleanupTrackedPools()

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			if err := test.TestFn(t, f, ctx, mcTctx, ns); err != nil {
				t.Error(err)
			}
		})

	}
}

func cleanupTestEnv(t *testing.T, ctx *framework.TestCtx) {
	// If the tests didn't fail, clean up. Else, leave everything
	// there so developers can debug the issue.
	if !t.Failed() {
		t.Log("The tests passed. Cleaning up.")
		ctx.Cleanup()
	} else {
		t.Log("The tests failed. Leaving the env there so you can debug.")
	}
}

// setupTestRequirements Adds the items to the client's schema (So we can use our objects in the client)
// and creates a test context.
//
// NOTE: Whenever we add new types to the operator, we need to register them here for the e2e tests.
func setupTestRequirements(t *testing.T) *framework.TestCtx {
	objects := [3]runtime.Object{&complianceoperatorv1alpha1.ComplianceScanList{},
		&complianceoperatorv1alpha1.ComplianceRemediationList{},
		&complianceoperatorv1alpha1.ComplianceSuiteList{}}
	for _, obj := range objects {
		err := framework.AddToFrameworkScheme(apis.AddToScheme, obj)
		if err != nil {
			t.Fatalf("TEST SETUP: failed to add custom resource scheme to framework: %v", err)
		}
	}
	return framework.NewTestCtx(t)
}

// setupComplianceOperatorCluster creates a compliance-operator cluster and the resources it needs to operate
// such as the namespace, permissions, etc.
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

// waitForScanStatus will poll until the compliancescan that we're lookingfor reaches a certain status, or until
// a timeout is reached.
func waitForScanStatus(t *testing.T, f *framework.Framework, namespace, name string, targetStatus complianceoperatorv1alpha1.ComplianceScanStatusPhase) error {
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

		if exampleComplianceScan.Status.Phase == targetStatus {
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

// waitForScanStatus will poll until the compliancescan that we're lookingfor reaches a certain status, or until
// a timeout is reached.
func waitForSuiteScansStatus(t *testing.T, f *framework.Framework, namespace, name string, targetStatus complianceoperatorv1alpha1.ComplianceScanStatusPhase) error {
	suite := &complianceoperatorv1alpha1.ComplianceSuite{}
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(retryInterval, timeout, func() (bool, error) {
		lastErr = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, suite)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				t.Logf("Waiting for availability of %s compliancesuite\n", name)
				return false, nil
			}
			t.Logf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		// Got the suite. There should be at least one scan or else we're still initialising
		if len(suite.Status.ScanStatuses) < 1 {
			return false, nil
		}

		//Examine the scan status both in the suite status and the scan
		for _, scanStatus := range suite.Status.ScanStatuses {
			if scanStatus.Phase != targetStatus {
				t.Logf("Waiting until scan %s is done", scanStatus.Name)
				return false, nil
			}

			lastErr = waitForScanStatus(t, f, namespace, scanStatus.Name, targetStatus)
			if lastErr != nil {
				// If the status was present in the suite, then /any/ error
				// should fail the test as the scans should be read /from/
				// the scan itself
				return true, nil
			}
		}

		return true, nil
	})

	// Error in function call
	if lastErr != nil {
		return lastErr
	}

	// Timeout
	if timeouterr != nil {
		return timeouterr
	}

	t.Logf("All scans in ComplianceSuite have finished (%s)\n", suite.Name)
	return nil
}

func scanResultIsExpected(f *framework.Framework, namespace, name string, expectedResult complianceoperatorv1alpha1.ComplianceScanStatusResult) error {
	cs := &complianceoperatorv1alpha1.ComplianceScan{}
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	if cs.Status.Result != expectedResult {
		return fmt.Errorf("The ComplianceScan Result wasn't what we expected. Got '%s', expected '%s'", cs.Status.Result, expectedResult)
	}
	if expectedResult == complianceoperatorv1alpha1.ResultError {
		if cs.Status.ErrorMessage == "" {
			return fmt.Errorf("The ComplianceScan 'errormsg' wasn't set (it was empty). Even if we expected an error.")
		}
	}
	return nil
}

// getNodesWithSelector lists nodes according to a specific selector
func getNodesWithSelector(f *framework.Framework, labelselector map[string]string) []corev1.Node {
	var nodes corev1.NodeList
	lo := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labelselector),
	}
	f.Client.List(goctx.TODO(), &nodes, lo)
	return nodes.Items
}

func getPodsForScan(f *framework.Framework, scanName string) ([]corev1.Pod, error) {
	selectPods := map[string]string{
		"complianceScan": scanName,
	}
	var pods corev1.PodList
	lo := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(selectPods),
	}
	err := f.Client.List(goctx.TODO(), &pods, lo)
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// getConfigMapsFromScan lists the configmaps from the specified openscap scan instance
func getConfigMapsFromScan(f *framework.Framework, scaninstance *complianceoperatorv1alpha1.ComplianceScan) []corev1.ConfigMap {
	var configmaps corev1.ConfigMapList
	labelselector := map[string]string{
		"compliance-scan": scaninstance.Name,
	}
	lo := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labelselector),
	}
	f.Client.List(goctx.TODO(), &configmaps, lo)
	return configmaps.Items
}

func getRemediationsFromScan(f *framework.Framework, suiteName, scanName string) []complianceoperatorv1alpha1.ComplianceRemediation {
	var scanSuiteRemediations complianceoperatorv1alpha1.ComplianceRemediationList

	scanSuiteSelector := make(map[string]string)
	scanSuiteSelector[complianceoperatorv1alpha1.SuiteLabel] = suiteName
	scanSuiteSelector[complianceoperatorv1alpha1.ScanLabel] = scanName

	listOpts := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(scanSuiteSelector),
	}

	f.Client.List(goctx.TODO(), &scanSuiteRemediations, &listOpts)
	return scanSuiteRemediations.Items
}

func assertHasRemediations(t *testing.T, f *framework.Framework, suiteName, scanName, roleLabel string, remNameList []string) error {
	var scanSuiteMapNames = make(map[string]bool)
	var scanSuiteRemediations []complianceoperatorv1alpha1.ComplianceRemediation

	// FIXME: This is a temporary hack. At the moment, the ARF parser is too slow
	// and it might take a bit for the remediations to appear. It would be cleaner
	// to signify somehow that the remediations were already processed, but in the
	// meantime, poll for 5 minutes while the remediations are being created
	err := wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		scanSuiteRemediations = getRemediationsFromScan(f, suiteName, scanName)
		for _, rem := range scanSuiteRemediations {
			scanSuiteMapNames[rem.Name] = true
		}

		for _, expRem := range remNameList {
			_, ok := scanSuiteMapNames[expRem]
			if !ok {
				t.Logf("expected remediation %s not yet found", expRem)
				return false, nil
			}
		}
		t.Logf("expected remediations found!")
		return true, nil
	})

	if err != nil {
		t.Errorf("Error waiting for remediations to appear")
		return err
	}

	for _, rem := range scanSuiteRemediations {
		if rem.Labels[mcfgv1.MachineConfigRoleLabelKey] != roleLabel {
			return fmt.Errorf("expected that scan %s is labeled for role %s", scanName, roleLabel)
		}
	}
	return nil
}

type MachineConfigActionFunc func() error
type PoolPredicate func(t *testing.T, pool *mcfgv1.MachineConfigPool) (bool, error)

// waitForMachinePoolUpdate retrieves the original version of a MCP, then performs an
// action passed in as a parameter and then waits until a MCP passes a predicate
func waitForMachinePoolUpdate(t *testing.T, mcClient *mcfgClient.MachineconfigurationV1Client, name string, action MachineConfigActionFunc, predicate PoolPredicate) error {
	poolPre, err := mcClient.MachineConfigPools().Get(name, metav1.GetOptions{})
	if err != nil {
		t.Errorf("Could not find the pool pre update")
		return err
	}
	t.Logf("Pre-update, MC Pool %s has generation %d", poolPre.Name, poolPre.Status.ObservedGeneration)

	err = action()
	if err != nil {
		t.Errorf("Action failed %v", err)
		return err
	}

	// Should we make this configurable? Maybe 5 minutes is not enough time for slower clusters?
	err = wait.PollImmediate(10*time.Second, 20*time.Minute, func() (bool, error) {
		pool, err := mcClient.MachineConfigPools().Get(name, metav1.GetOptions{})
		if err != nil {
			// even not found is a hard error here
			t.Errorf("Could not find the pool post update")
			return false, err
		}

		ok, err := predicate(t, pool)
		if err != nil {
			t.Errorf("Predicate failed %v", err)
			return false, err
		}

		if !ok {
			t.Logf("Predicate not true yet, waiting")
			return false, nil
		}

		t.Logf("Will check for update, Gen: %d, previous %d updated %d/%d unavailable %d",
			pool.Status.ObservedGeneration, poolPre.Status.ObservedGeneration,
			pool.Status.UpdatedMachineCount, pool.Status.MachineCount,
			pool.Status.UnavailableMachineCount)

		// Check if the pool has finished updating yet. If the pool was paused, we just check that
		// the generation was increased and wait for machines to reboot separately
		if (pool.Status.ObservedGeneration != poolPre.Status.ObservedGeneration) &&
			pool.Spec.Paused == true || ((pool.Status.UpdatedMachineCount == pool.Status.MachineCount) &&
			(pool.Status.UnavailableMachineCount == 0)) {
			t.Logf("The pool has updated")
			return true, nil
		}

		t.Logf("The pool has not updated yet. Gen: %d, expected %d updated %d/%d unavailable %d",
			pool.Status.ObservedGeneration, poolPre.Status.ObservedGeneration,
			pool.Status.UpdatedMachineCount, pool.Status.MachineCount,
			pool.Status.UnavailableMachineCount)
		return false, nil
	})

	if err != nil {
		return err
	}

	return nil
}

// waitForNodesToBeReady waits until all the nodes in the cluster have
// reached the expected machineConfig.
func waitForNodesToBeReady(t *testing.T, f *framework.Framework) error {
	err := wait.PollImmediate(10*time.Second, timeout, func() (bool, error) {
		var nodes corev1.NodeList

		f.Client.List(goctx.TODO(), &nodes, &client.ListOptions{})
		for _, node := range nodes.Items {
			t.Logf("Node %s has config %s, desired config %s state %s",
				node.Name,
				node.Annotations["machineconfiguration.openshift.io/currentConfig"],
				node.Annotations["machineconfiguration.openshift.io/desiredConfig"],
				node.Annotations["machineconfiguration.openshift.io/state"])

			if (node.Annotations["machineconfiguration.openshift.io/currentConfig"] != node.Annotations["machineconfiguration.openshift.io/desiredConfig"]) ||
				(node.Annotations["machineconfiguration.openshift.io/state"] != "Done") {
				t.Logf("Node %s still updating", node.Name)
				return false, nil
			}
			t.Logf("Node %s was updated", node.Name)
		}

		t.Logf("All machines updated")
		return true, nil
	})

	if err != nil {
		return err
	}

	return nil
}

// waitForNodesToHaveARenderedPool wait until all nodes passed through a parameter transition to a rendered
// config from a pool that is passed through a parameter as well. A typical use-case is when a node is unlabeled
// from a pool, in that case we need to wait until MCO makes the node use the other available pool. Only then it
// is safe to remove the pool the node was labeled with, otherwise the node might still on next reboot use the
// pool that was removed and this would mean the node transitions into Degraded state
func waitForNodesToHaveARenderedPool(t *testing.T, f *framework.Framework, mcClient *mcfgClient.MachineconfigurationV1Client, nodes []corev1.Node, poolName string) error {
	pool, err := mcClient.MachineConfigPools().Get(poolName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("Could not find pool %s\n", poolName)
		return err
	}

	t.Logf("We'll wait for the nodes to reach %s\n", pool.Spec.Configuration.Name)
	return wait.PollImmediate(10*time.Second, timeout, func() (bool, error) {
		for _, loopNode := range nodes {
			node := &corev1.Node{}
			err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: loopNode.Name}, node)
			if err != nil {
				return false, err
			}

			t.Logf("Node %s has config %s, desired config %s state %s",
				node.Name,
				node.Annotations["machineconfiguration.openshift.io/currentConfig"],
				node.Annotations["machineconfiguration.openshift.io/desiredConfig"],
				node.Annotations["machineconfiguration.openshift.io/state"])

			if node.Annotations["machineconfiguration.openshift.io/desiredConfig"] != pool.Spec.Configuration.Name ||
				node.Annotations["machineconfiguration.openshift.io/currentConfig"] != node.Annotations["machineconfiguration.openshift.io/desiredConfig"] {
				t.Logf("Node %s still updating", node.Name)
				return false, nil
			}
			t.Logf("Node %s was updated", node.Name)
		}

		t.Logf("All machines updated")
		return true, nil
	})
}

func applyRemediationAndCheck(t *testing.T, f *framework.Framework, mcClient *mcfgClient.MachineconfigurationV1Client, namespace, name, pool string) error {
	rem := &complianceoperatorv1alpha1.ComplianceRemediation{}
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem)
	if err != nil {
		return err
	}
	t.Logf("Remediation %s found", name)

	applyRemediation := func() error {
		rem.Spec.Apply = true
		err = f.Client.Update(goctx.TODO(), rem)
		if err != nil {
			t.Errorf("Cannot apply remediation")
			return err
		}
		t.Logf("Remediation applied")
		return nil
	}

	predicate := func(t *testing.T, pool *mcfgv1.MachineConfigPool) (bool, error) {
		// When checking if a MC is applied to a pool, we can't check the pool status
		// when the pool is paused..
		source := pool.Status.Configuration.Source
		if pool.Spec.Paused == true {
			source = pool.Spec.Configuration.Source
		}

		for _, mc := range source {
			if mc.Name == rem.GetMcName() {
				// When applying a remediation, check that the MC *is* in the pool
				t.Logf("Remediation %s present in pool %s, returning true", mc.Name, pool.Name)
				return true, nil
			}
		}

		t.Logf("Remediation %s not present in pool %s, returning false", rem.GetMcName(), pool.Name)
		return false, nil
	}

	err = waitForMachinePoolUpdate(t, mcClient, pool, applyRemediation, predicate)
	if err != nil {
		t.Errorf("Failed to wait for pool to update after applying MC: %v", err)
		return err
	}

	t.Logf("Machines updated with remediation")
	return nil
}

func applyRemediationAndWaitForReboot(t *testing.T, f *framework.Framework, mcClient *mcfgClient.MachineconfigurationV1Client, namespace, name, pool string) error {
	err := applyRemediationAndCheck(t, f, mcClient, namespace, name, pool)
	if err != nil {
		t.Errorf("Failed to apply remediation and check for MCP update: %v", err)
		return err
	}

	err = waitForNodesToBeReady(t, f)
	if err != nil {
		t.Errorf("Failed to wait for nodes to come back up after applying MC: %v", err)
		return err
	}

	t.Logf("Remediation applied to machines and machines rebooted")
	return nil
}

func unApplyRemediationAndCheck(t *testing.T, f *framework.Framework, mcClient *mcfgClient.MachineconfigurationV1Client, namespace, name, pool string, lastRemediation bool) error {
	rem := &complianceoperatorv1alpha1.ComplianceRemediation{}
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem)
	if err != nil {
		return err
	}
	t.Logf("Remediation found")

	applyRemediation := func() error {
		rem.Spec.Apply = false
		err = f.Client.Update(goctx.TODO(), rem)
		if err != nil {
			t.Errorf("Cannot apply remediation")
			return err
		}
		t.Logf("Remediation applied")
		return nil
	}

	predicate := func(t *testing.T, pool *mcfgv1.MachineConfigPool) (bool, error) {
		// If the remediation that we deselect is NOT the last one, it is expected
		// that the MC would still be present. Just return true in this case.
		if lastRemediation == false {
			return true, nil
		}

		// On the other hand, if the remediation we deselect WAS the last one, we want
		// to check that the MC created by the operator went away. In that case, let's
		// poll the pool until we no longer see the remediation in the status
		for _, mc := range pool.Status.Configuration.Source {
			if mc.Name == rem.GetMcName() {
				t.Logf("Remediation %s present in pool %s, returning false", mc.Name, pool.Name)
				return false, nil
			}
		}

		t.Logf("Remediation %s not present in pool %s, returning true", rem.GetMcName(), pool.Name)
		return true, nil
	}

	err = waitForMachinePoolUpdate(t, mcClient, pool, applyRemediation, predicate)
	if err != nil {
		t.Errorf("Failed to wait for pool to update after applying MC: %v", err)
		return err
	}

	t.Logf("Machines updated with remediation")
	return nil
}

func unPauseMachinePoolAndWait(t *testing.T, mcClient *mcfgClient.MachineconfigurationV1Client, poolName string) error {
	err := unPauseMachinePool(t, mcClient, poolName)
	if err != nil {
		t.Errorf("Could not unpause the MC pool")
		return err
	}

	// When the pool updates, we need to wait for the machines to pick up the new rendered
	// config
	err = wait.PollImmediate(10*time.Second, 20*time.Minute, func() (bool, error) {
		pool, err := mcClient.MachineConfigPools().Get(poolName, metav1.GetOptions{})
		if err != nil {
			// even not found is a hard error here
			t.Errorf("Could not find the pool post update")
			return false, err
		}

		t.Logf("Will check for update, updated %d/%d unavailable %d",
			pool.Status.UpdatedMachineCount, pool.Status.MachineCount,
			pool.Status.UnavailableMachineCount)

		if pool.Status.UpdatedMachineCount == pool.Status.MachineCount &&
			pool.Status.UnavailableMachineCount == 0 {
			t.Logf("The pool has updated")
			return true, nil
		}

		t.Logf("The pool has not updated yet. updated %d/%d unavailable %d",
			pool.Status.UpdatedMachineCount, pool.Status.MachineCount,
			pool.Status.UnavailableMachineCount)
		return false, nil
	})

	return err
}

func pauseMachinePool(t *testing.T, mcClient *mcfgClient.MachineconfigurationV1Client, poolName string) error {
	return modMachinePoolPause(t, mcClient, poolName, true)
}

func unPauseMachinePool(t *testing.T, mcClient *mcfgClient.MachineconfigurationV1Client, poolName string) error {
	return modMachinePoolPause(t, mcClient, poolName, false)
}

func modMachinePoolPause(t *testing.T, mcClient *mcfgClient.MachineconfigurationV1Client, poolName string, pause bool) error {
	pool, err := mcClient.MachineConfigPools().Get(poolName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("Could not find the pool to modify")
		return err
	}

	poolCopy := pool.DeepCopy()
	poolCopy.Spec.Paused = pause
	_, err = mcClient.MachineConfigPools().Update(poolCopy)
	if err != nil {
		t.Errorf("Could not update the pool")
		return err
	}

	return nil
}

func createReadyMachineConfigPoolSubset(t *testing.T, f *framework.Framework, mcClient *mcfgClient.MachineconfigurationV1Client, oldPoolName, newPoolName string) (*mcfgv1.MachineConfigPool, error) {
	pool, err := createMachineConfigPoolSubset(t, f, mcClient, oldPoolName, newPoolName)
	if err != nil {
		return nil, err
	}

	err = waitForPoolCondition(t, mcClient, mcfgv1.MachineConfigPoolUpdated, newPoolName)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// picks a random machine from an existing pool and creates a subset of the pool with
// one machine
func createMachineConfigPoolSubset(t *testing.T, f *framework.Framework, mcClient *mcfgClient.MachineconfigurationV1Client, oldPoolName, newPoolName string) (*mcfgv1.MachineConfigPool, error) {
	// retrieve the old pool
	oldPool, err := mcClient.MachineConfigPools().Get(oldPoolName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("Could not find the pool to modify")
		return nil, err
	}

	// list the nodes matching the node selector
	poolNodes := getNodesWithSelector(f, oldPool.Spec.NodeSelector.MatchLabels)
	if len(poolNodes) == 0 {
		return nil, errors.New("no nodes found with the old pool selector")
	}

	// just pick one of them and create the new pool out of that one-item node slice
	return createMachineConfigPool(t, f, mcClient, oldPoolName, newPoolName, poolNodes[:1])
}

// creates a new pool named newPoolName from a list of nodes
func createMachineConfigPool(t *testing.T, f *framework.Framework, mcClient *mcfgClient.MachineconfigurationV1Client, oldPoolName, newPoolName string, nodes []corev1.Node) (*mcfgv1.MachineConfigPool, error) {
	newPoolNodeLabel := fmt.Sprintf("node-role.kubernetes.io/%s", newPoolName)

	err := labelNodes(t, f, newPoolNodeLabel, nodes)
	if err != nil {
		return nil, err
	}

	return createMCPObject(mcClient, newPoolNodeLabel, oldPoolName, newPoolName)
}

func labelNodes(t *testing.T, f *framework.Framework, newPoolNodeLabel string, nodes []corev1.Node) error {
	for _, node := range nodes {
		nodeCopy := node.DeepCopy()
		nodeCopy.Labels[newPoolNodeLabel] = ""

		t.Logf("Adding label %s to node %s\n", newPoolNodeLabel, nodeCopy.Name)
		err := f.Client.Update(goctx.TODO(), nodeCopy)
		if err != nil {
			t.Logf("Could not label node %s with %s\n", nodeCopy.Name, newPoolNodeLabel)
			return err
		}
	}

	return nil
}

func unLabelNodes(t *testing.T, f *framework.Framework, rmPoolNodeLabel string, nodes []corev1.Node) error {
	for _, node := range nodes {
		nodeCopy := node.DeepCopy()
		delete(nodeCopy.Labels, rmPoolNodeLabel)

		t.Logf("Removing label %s from node %s\n", rmPoolNodeLabel, nodeCopy.Name)
		err := f.Client.Update(goctx.TODO(), nodeCopy)
		if err != nil {
			t.Logf("Could not label node %s with %s\n", nodeCopy.Name, rmPoolNodeLabel)
			return err
		}
	}

	return nil
}

func createMCPObject(mcClient *mcfgClient.MachineconfigurationV1Client, newPoolNodeLabel, oldPoolName, newPoolName string) (*mcfgv1.MachineConfigPool, error) {
	nodeSelectorMatchLabel := make(map[string]string)
	nodeSelectorMatchLabel[newPoolNodeLabel] = ""

	newPool := mcfgv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{Name: newPoolName},
		Spec: mcfgv1.MachineConfigPoolSpec{
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: nodeSelectorMatchLabel,
			},
			MachineConfigSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      mcfgv1.MachineConfigRoleLabelKey,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{oldPoolName, newPoolName},
					},
				},
			},
		},
	}

	return mcClient.MachineConfigPools().Create(&newPool)
}

func waitForPoolCondition(t *testing.T, mcClient *mcfgClient.MachineconfigurationV1Client, conditionType mcfgv1.MachineConfigPoolConditionType, newPoolName string) error {
	return wait.PollImmediate(10*time.Second, 20*time.Minute, func() (bool, error) {
		pool, err := mcClient.MachineConfigPools().Get(newPoolName, metav1.GetOptions{})
		if err != nil {
			t.Errorf("Could not find the pool post update")
			return false, err
		}

		if isMachineConfigPoolConditionTrue(pool.Status.Conditions, conditionType) {
			return true, nil
		}

		t.Logf("The pool has not updated yet\n")
		return false, nil
	})
}

// isMachineConfigPoolConditionTrue returns true when the conditionType is present and set to `ConditionTrue`
func isMachineConfigPoolConditionTrue(conditions []mcfgv1.MachineConfigPoolCondition, conditionType mcfgv1.MachineConfigPoolConditionType) bool {
	return IsMachineConfigPoolConditionPresentAndEqual(conditions, conditionType, corev1.ConditionTrue)
}

// IsMachineConfigPoolConditionPresentAndEqual returns true when conditionType is present and equal to status.
func IsMachineConfigPoolConditionPresentAndEqual(conditions []mcfgv1.MachineConfigPoolCondition, conditionType mcfgv1.MachineConfigPoolConditionType, status corev1.ConditionStatus) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == status
		}
	}
	return false
}

func E2EPoolNodeRoleSelector() map[string]string {
	return utils.GetNodeRoleSelector(testPoolName)
}
