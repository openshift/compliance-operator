package e2e

import (
	goctx "context"
	"errors"
	"fmt"
	"regexp"
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
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/utils"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	workerPoolName = "worker"
	testPoolName   = "e2e"
)

type testExecution struct {
	Name   string
	TestFn func(*testing.T, *framework.Framework, *framework.Context, *mcTestCtx, string) error
}

type mcTestCtx struct {
	f     *framework.Framework
	t     *testing.T
	pools []*mcfgv1.MachineConfigPool
}

func newMcTestCtx(f *framework.Framework, t *testing.T) (*mcTestCtx, error) {
	return &mcTestCtx{f: f, t: t}, nil
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
		err = waitForNodesToHaveARenderedPool(c.t, c.f, poolNodes, workerPoolName)
		if err != nil {
			c.t.Errorf("Error waiting for nodes to reach the worker pool again: %v\n", err)
		}

		err = waitForPoolCondition(c.t, c.f, mcfgv1.MachineConfigPoolUpdated, p.Name)
		if err != nil {
			c.t.Errorf("Error waiting for reboot after nodes were unlabeled: %v\n", err)
		}

		// Then delete the pool itself
		c.t.Logf("Removing pool %s\n", p.Name)
		err = c.f.Client.Delete(goctx.TODO(), p)
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
	pool, err := createReadyMachineConfigPoolSubset(c.t, c.f, workerPoolName, testPoolName)
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
	defer ctx.Cleanup()

	setupComplianceOperatorCluster(t, ctx)

	// get global framework variables
	f := framework.Global

	ns, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("could not get namespace: %v", err)
	}

	mcTctx, err := newMcTestCtx(f, t)
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

// setupTestRequirements Adds the items to the client's schema (So we can use our objects in the client)
// and creates a test context.
//
// NOTE: Whenever we add new types to the operator, we need to register them here for the e2e tests.
func setupTestRequirements(t *testing.T) *framework.Context {
	// compliance-operator objects
	coObjs := [3]runtime.Object{&compv1alpha1.ComplianceScanList{},
		&compv1alpha1.ComplianceRemediationList{},
		&compv1alpha1.ComplianceSuiteList{},
	}
	for _, obj := range coObjs {
		err := framework.AddToFrameworkScheme(apis.AddToScheme, obj)
		if err != nil {
			t.Fatalf("TEST SETUP: failed to add custom resource scheme to framework: %v", err)
		}
	}
	// MCO objects
	mcoObjs := [2]runtime.Object{
		&mcfgv1.MachineConfigPoolList{},
		&mcfgv1.MachineConfigList{},
	}
	for _, obj := range mcoObjs {
		err := framework.AddToFrameworkScheme(mcfgapi.Install, obj)
		if err != nil {
			t.Fatalf("TEST SETUP: failed to add custom resource scheme to framework: %v", err)
		}
	}
	return framework.NewContext(t)
}

// setupComplianceOperatorCluster creates a compliance-operator cluster and the resources it needs to operate
// such as the namespace, permissions, etc.
func setupComplianceOperatorCluster(t *testing.T, ctx *framework.Context) {
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
func waitForScanStatus(t *testing.T, f *framework.Framework, namespace, name string, targetStatus compv1alpha1.ComplianceScanStatusPhase) error {
	exampleComplianceScan := &compv1alpha1.ComplianceScan{}
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

// waitForReScanStatus will poll until the compliancescan that we're lookingfor reaches a certain status for a re-scan, or until
// a timeout is reached.
func waitForReScanStatus(t *testing.T, f *framework.Framework, namespace, name string, targetStatus compv1alpha1.ComplianceScanStatusPhase) error {
	foundScan := &compv1alpha1.ComplianceScan{}
	// unset initial index
	var scanIndex int64 = -1
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(retryInterval, timeout, func() (bool, error) {
		lastErr = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, foundScan)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				t.Logf("Waiting for availability of %s compliancescan\n", name)
				return false, nil
			}
			t.Logf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}
		// Set index
		if scanIndex == -1 {
			scanIndex = foundScan.Status.CurrentIndex
			t.Logf("Initial scan index set to %d. Waiting for re-scan\n", scanIndex)
			return false, nil
		} else if foundScan.Status.CurrentIndex == scanIndex {
			t.Logf("re-scan hasn't taken place. CurrentIndex %d. Waiting for re-scan\n", scanIndex)
			return false, nil
		}

		if foundScan.Status.Phase == targetStatus {
			return true, nil
		}
		t.Logf("Waiting for run of %s compliancescan (%s)\n", name, foundScan.Status.Phase)
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
	t.Logf("ComplianceScan ready (%s)\n", foundScan.Status.Phase)
	return nil
}

// waitForRemediationState will poll until the complianceRemediation that we're lookingfor gets applied, or until
// a timeout is reached.
func waitForRemediationState(t *testing.T, f *framework.Framework, namespace, name string, state compv1alpha1.RemediationApplicationState) error {
	rem := &compv1alpha1.ComplianceRemediation{}
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(retryInterval, timeout, func() (bool, error) {
		lastErr = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				t.Logf("Waiting for availability of %s ComplianceRemediation\n", name)
				return false, nil
			}
			t.Logf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		if rem.Status.ApplicationState == state {
			return true, nil
		}
		t.Logf("Waiting for run of %s ComplianceRemediation (%s)\n", name, rem.Status.ApplicationState)
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
	t.Logf("ComplianceRemediation ready (%s)\n", rem.Status.ApplicationState)
	return nil
}

func waitForObjectToExist(t *testing.T, f *framework.Framework, name, namespace string, obj runtime.Object) error {
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(retryInterval, timeout, func() (bool, error) {
		lastErr = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, obj)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				t.Logf("Waiting for availability of %s ComplianceRemediation\n", name)
				return false, nil
			}
			t.Logf("Retrying. Got error: %v\n", lastErr)
			return false, nil
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

	t.Logf("Object found '%s' found\n", name)
	return nil
}

// waitForScanStatus will poll until the compliancescan that we're lookingfor reaches a certain status, or until
// a timeout is reached.
func waitForSuiteScansStatus(t *testing.T, f *framework.Framework, namespace, name string, targetStatus compv1alpha1.ComplianceScanStatusPhase, targetComplianceStatus compv1alpha1.ComplianceScanStatusResult) error {
	suite := &compv1alpha1.ComplianceSuite{}
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

		if suite.Status.AggregatedPhase != targetStatus {
			t.Logf("Waiting until suite %s reaches target status '%s'. Current status: %s", suite.Name, targetStatus, suite.Status.AggregatedPhase)
			return false, nil
		}

		// The suite is now done, make sure the compliance status is expected
		if suite.Status.AggregatedResult != targetComplianceStatus {
			return false, fmt.Errorf("expecting %s got %s", targetComplianceStatus, suite.Status.AggregatedResult)
		}

		// If we were expecting an error, there's no use checking the scans
		if targetComplianceStatus == compv1alpha1.ResultError {
			return true, nil
		}

		// Now as a sanity check make sure that the scan statuses match the aggregated
		// suite status

		// Got the suite. There should be at least one scan or else we're still initialising
		if len(suite.Status.ScanStatuses) < 1 {
			return false, errors.New("not enough scan statuses")
		}

		//Examine the scan status both in the suite status and the scan
		for _, scanStatus := range suite.Status.ScanStatuses {
			if scanStatus.Phase != targetStatus {
				return false, fmt.Errorf("suite in status %s but scan wrapper %s in status %s", targetStatus, scanStatus.Name, scanStatus.Phase)
			}

			lastErr = waitForScanStatus(t, f, namespace, scanStatus.Name, targetStatus)
			if lastErr != nil {
				// If the status was present in the suite, then /any/ error
				// should fail the test as the scans should be read /from/
				// the scan itself
				return true, fmt.Errorf("suite in status %s but scan object %s in status %s", targetStatus, scanStatus.Name, scanStatus.Phase)
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

func scanResultIsExpected(f *framework.Framework, namespace, name string, expectedResult compv1alpha1.ComplianceScanStatusResult) error {
	cs := &compv1alpha1.ComplianceScan{}
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	if cs.Status.Result != expectedResult {
		return fmt.Errorf("The ComplianceScan Result wasn't what we expected. Got '%s', expected '%s'", cs.Status.Result, expectedResult)
	}
	if expectedResult == compv1alpha1.ResultError {
		if cs.Status.ErrorMessage == "" {
			return fmt.Errorf("The ComplianceScan 'errormsg' wasn't set (it was empty). Even if we expected an error")
		}
	}
	return nil
}

func suiteErrorMessageMatchesRegex(t *testing.T, f *framework.Framework, namespace, name, regexToMatch string) error {
	t.Logf("Fetching suite: '%s'", name)
	cs := &compv1alpha1.ComplianceSuite{}
	key := types.NamespacedName{Name: name, Namespace: namespace}
	err := f.Client.Get(goctx.TODO(), key, cs)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(regexToMatch)
	if !re.MatchString(cs.Status.ErrorMessage) {
		return fmt.Errorf("The error message found in the compliance suite '%s' "+
			"didn't match the expected regex. Found: '%s', Expected regex: '%s'",
			name, cs.Status.ErrorMessage, regexToMatch)
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
func getConfigMapsFromScan(f *framework.Framework, scaninstance *compv1alpha1.ComplianceScan) []corev1.ConfigMap {
	var configmaps corev1.ConfigMapList
	labelselector := map[string]string{
		compv1alpha1.ComplianceScanIndicatorLabel: scaninstance.Name,
	}
	lo := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labelselector),
	}
	f.Client.List(goctx.TODO(), &configmaps, lo)
	return configmaps.Items
}

func assertHasCheck(f *framework.Framework, suiteName, scanName string, check compv1alpha1.ComplianceCheckResult) error {
	var getCheck compv1alpha1.ComplianceCheckResult

	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: check.Name, Namespace: check.Namespace}, &getCheck)
	if err != nil {
		return err
	}

	if getCheck.Status != check.Status {
		return fmt.Errorf("expected result %s got result %s", check.Status, getCheck.Status)
	}

	if getCheck.ID != check.ID {
		return fmt.Errorf("expected ID %s got ID %s", check.ID, getCheck.ID)
	}

	if getCheck.Labels == nil {
		return fmt.Errorf("complianceCheckResult has no labels")
	}

	if getCheck.Labels[compv1alpha1.SuiteLabel] != suiteName {
		return fmt.Errorf("Did not find expected suite name label %s, found %s", suiteName, getCheck.Labels[compv1alpha1.SuiteLabel])
	}

	if getCheck.Labels[compv1alpha1.ScanLabel] != scanName {
		return fmt.Errorf("Did not find expected suite name label %s, found %s", suiteName, getCheck.Labels[compv1alpha1.SuiteLabel])
	}

	if getCheck.Labels[compv1alpha1.ComplianceCheckResultSeverityLabel] != string(getCheck.Severity) {
		return fmt.Errorf("did not find expected severity name label %s, found %s", suiteName, getCheck.Labels[compv1alpha1.ComplianceCheckResultSeverityLabel])
	}

	if getCheck.Labels[compv1alpha1.ComplianceCheckResultStatusLabel] != string(getCheck.Status) {
		return fmt.Errorf("did not find expected status name label %s, found %s", suiteName, getCheck.Labels[compv1alpha1.ComplianceCheckResultStatusLabel])
	}

	return nil
}

func getRemediationsFromScan(f *framework.Framework, suiteName, scanName string) []compv1alpha1.ComplianceRemediation {
	var scanSuiteRemediations compv1alpha1.ComplianceRemediationList

	scanSuiteSelector := make(map[string]string)
	scanSuiteSelector[compv1alpha1.SuiteLabel] = suiteName
	scanSuiteSelector[compv1alpha1.ScanLabel] = scanName

	listOpts := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(scanSuiteSelector),
	}

	f.Client.List(goctx.TODO(), &scanSuiteRemediations, &listOpts)
	return scanSuiteRemediations.Items
}

func assertHasRemediations(t *testing.T, f *framework.Framework, suiteName, scanName, roleLabel string, remNameList []string) error {
	var scanSuiteMapNames = make(map[string]bool)
	var scanSuiteRemediations []compv1alpha1.ComplianceRemediation

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

type machineConfigActionFunc func() error
type poolPredicate func(t *testing.T, pool *mcfgv1.MachineConfigPool) (bool, error)

// waitForMachinePoolUpdate retrieves the original version of a MCP, then performs an
// action passed in as a parameter and then waits until a MCP passes a predicate
// If a pool is already given (poolPre), that will be used to check the previous state of the pool.
func waitForMachinePoolUpdate(t *testing.T, f *framework.Framework, name string, action machineConfigActionFunc, predicate poolPredicate, poolPre *mcfgv1.MachineConfigPool) error {
	if poolPre == nil {
		// initialize empty pool if it wasn't already given
		poolPre = &mcfgv1.MachineConfigPool{}
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name}, poolPre)
		if err != nil {
			t.Errorf("Could not find the pool pre update")
			return err
		}
	}
	t.Logf("Pre-update, MC Pool %s has generation %d", poolPre.Name, poolPre.Status.ObservedGeneration)

	err := action()
	if err != nil {
		t.Errorf("Action failed %v", err)
		return err
	}

	// Should we make this configurable? Maybe 5 minutes is not enough time for slower clusters?
	err = wait.PollImmediate(10*time.Second, 20*time.Minute, func() (bool, error) {
		pool := &mcfgv1.MachineConfigPool{}
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name}, pool)
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
func waitForNodesToHaveARenderedPool(t *testing.T, f *framework.Framework, nodes []corev1.Node, poolName string) error {
	pool := &mcfgv1.MachineConfigPool{}
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: poolName}, pool)
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

func applyRemediationAndCheck(t *testing.T, f *framework.Framework, namespace, name, pool string) error {
	rem := &compv1alpha1.ComplianceRemediation{}
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

	err = waitForMachinePoolUpdate(t, f, pool, applyRemediation, predicate, nil)
	if err != nil {
		t.Errorf("Failed to wait for pool to update after applying MC: %v", err)
		return err
	}

	t.Logf("Machines updated with remediation")
	return nil
}

func unApplyRemediationAndCheck(t *testing.T, f *framework.Framework, namespace, name, pool string, lastRemediation bool) error {
	rem := &compv1alpha1.ComplianceRemediation{}
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

	err = waitForMachinePoolUpdate(t, f, pool, applyRemediation, predicate, nil)
	if err != nil {
		t.Errorf("Failed to wait for pool to update after applying MC: %v", err)
		return err
	}

	t.Logf("Machines updated with remediation")
	return nil
}

func waitForRemediationToBeAutoApplied(t *testing.T, f *framework.Framework, remName, remNamespace string, pool *mcfgv1.MachineConfigPool) error {
	rem := &compv1alpha1.ComplianceRemediation{}
	var lastErr error
	timeouterr := wait.Poll(retryInterval, timeout, func() (bool, error) {
		lastErr = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: remName, Namespace: remNamespace}, rem)
		if apierrors.IsNotFound(lastErr) {
			t.Logf("Waiting for availability of %s remediation\n", remName)
			return false, nil
		}
		if lastErr != nil {
			t.Logf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}
		t.Logf("Found remediation: %s\n", remName)
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

	preNoop := func() error {
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

	err := waitForMachinePoolUpdate(t, f, pool.Name, preNoop, predicate, pool)
	if err != nil {
		t.Errorf("Failed to wait for pool to update after applying MC: %v", err)
		return err
	}

	t.Logf("Machines updated with remediation")
	err = waitForNodesToBeReady(t, f)
	if err != nil {
		t.Errorf("Failed to wait for nodes to come back up after applying MC: %v", err)
		return err
	}

	t.Logf("Remediation applied to machines and machines rebooted")
	return nil
}

func unPauseMachinePoolAndWait(t *testing.T, f *framework.Framework, poolName string) error {
	err := unPauseMachinePool(t, f, poolName)
	if err != nil {
		t.Errorf("Could not unpause the MC pool")
		return err
	}

	// When the pool updates, we need to wait for the machines to pick up the new rendered
	// config
	err = wait.PollImmediate(10*time.Second, 20*time.Minute, func() (bool, error) {
		pool := &mcfgv1.MachineConfigPool{}
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: poolName}, pool)
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

func pauseMachinePool(t *testing.T, f *framework.Framework, poolName string) error {
	return modMachinePoolPause(t, f, poolName, true)
}

func unPauseMachinePool(t *testing.T, f *framework.Framework, poolName string) error {
	return modMachinePoolPause(t, f, poolName, false)
}

func modMachinePoolPause(t *testing.T, f *framework.Framework, poolName string, pause bool) error {
	pool := &mcfgv1.MachineConfigPool{}
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: poolName}, pool)
	if err != nil {
		t.Errorf("Could not find the pool to modify")
		return err
	}

	poolCopy := pool.DeepCopy()
	poolCopy.Spec.Paused = pause
	err = f.Client.Update(goctx.TODO(), poolCopy)
	if err != nil {
		t.Errorf("Could not update the pool")
		return err
	}

	return nil
}

func createReadyMachineConfigPoolSubset(t *testing.T, f *framework.Framework, oldPoolName, newPoolName string) (*mcfgv1.MachineConfigPool, error) {
	pool, err := createMachineConfigPoolSubset(t, f, oldPoolName, newPoolName)
	if err != nil {
		return nil, err
	}

	err = waitForPoolCondition(t, f, mcfgv1.MachineConfigPoolUpdated, newPoolName)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// picks a random machine from an existing pool and creates a subset of the pool with
// one machine
func createMachineConfigPoolSubset(t *testing.T, f *framework.Framework, oldPoolName, newPoolName string) (*mcfgv1.MachineConfigPool, error) {
	// retrieve the old pool
	oldPool := &mcfgv1.MachineConfigPool{}
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: oldPoolName}, oldPool)
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
	return createMachineConfigPool(t, f, oldPoolName, newPoolName, poolNodes[:1])
}

// creates a new pool named newPoolName from a list of nodes
func createMachineConfigPool(t *testing.T, f *framework.Framework, oldPoolName, newPoolName string, nodes []corev1.Node) (*mcfgv1.MachineConfigPool, error) {
	newPoolNodeLabel := fmt.Sprintf("node-role.kubernetes.io/%s", newPoolName)

	err := labelNodes(t, f, newPoolNodeLabel, nodes)
	if err != nil {
		return nil, err
	}

	return createMCPObject(f, newPoolNodeLabel, oldPoolName, newPoolName)
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

func createMCPObject(f *framework.Framework, newPoolNodeLabel, oldPoolName, newPoolName string) (*mcfgv1.MachineConfigPool, error) {
	nodeSelectorMatchLabel := make(map[string]string)
	nodeSelectorMatchLabel[newPoolNodeLabel] = ""

	newPool := &mcfgv1.MachineConfigPool{
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

	// We create but don't clean up, we'll call a function for this since we need to
	// re-label hosts first.
	err := f.Client.Create(goctx.TODO(), newPool, nil)
	return newPool, err
}

func waitForPoolCondition(t *testing.T, f *framework.Framework, conditionType mcfgv1.MachineConfigPoolConditionType, newPoolName string) error {
	return wait.PollImmediate(10*time.Second, 20*time.Minute, func() (bool, error) {
		pool := mcfgv1.MachineConfigPool{}
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: newPoolName}, &pool)
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

func getPoolNodeRoleSelector() map[string]string {
	return utils.GetNodeRoleSelector(testPoolName)
}
