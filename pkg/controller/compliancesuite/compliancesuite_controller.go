package compliancesuite

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
)

var log = logf.Log.WithName("suitectrl")

// Add creates a new ComplianceSuite Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileComplianceSuite{client: mgr.GetClient(), scheme: mgr.GetScheme(), recorder: mgr.GetEventRecorderFor("suitectrl")}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("compliancesuite-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource ComplianceSuite
	err = c.Watch(&source.Kind{Type: &compv1alpha1.ComplianceSuite{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource ComplianceScans and requeue the owner ComplianceSuite
	err = c.Watch(&source.Kind{Type: &compv1alpha1.ComplianceScan{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &compv1alpha1.ComplianceSuite{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileComplianceSuite implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileComplianceSuite{}

// ReconcileComplianceSuite reconciles a ComplianceSuite object
type ReconcileComplianceSuite struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
}

// Reconcile reads that state of the cluster for a ComplianceSuite object and makes changes based on the state read
// and what is in the ComplianceSuite.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileComplianceSuite) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling ComplianceSuite")

	// Fetch the ComplianceSuite suite
	suite := &compv1alpha1.ComplianceSuite{}
	err := r.client.Get(context.TODO(), request.NamespacedName, suite)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Cannot get the suite")
		return reconcile.Result{}, err
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if suite.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !common.ContainsFinalizer(suite.ObjectMeta.Finalizers, compv1alpha1.SuiteFinalizer) {
			suite.ObjectMeta.Finalizers = append(suite.ObjectMeta.Finalizers, compv1alpha1.SuiteFinalizer)
			if err := r.client.Update(context.TODO(), suite); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		return reconcile.Result{}, r.suiteDeleteHandler(suite, reqLogger)
	}

	if isValid, errorMsg := r.validateSuite(suite); !isValid {
		// return immediately and don't schedule nor reconcile scans
		return reconcile.Result{}, r.issueValidationError(suite, errorMsg, reqLogger)
	}

	suiteCopy := suite.DeepCopy()
	if err := r.reconcileScans(suiteCopy, reqLogger); err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	var res reconcile.Result
	if res, err = r.reconcileRemediations(suiteCopy, reqLogger); err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	if suiteCopy.IsResultAvailable() {
		return res, r.reconcileScanRerunnerCronJob(suiteCopy, reqLogger)
	}

	return res, nil
}

func (r *ReconcileComplianceSuite) suiteDeleteHandler(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	rerunner := getRerunner(suite)
	err := r.client.Delete(context.TODO(), rerunner)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	suiteCopy := suite.DeepCopy()
	// remove our finalizer from the list and update it.
	suiteCopy.ObjectMeta.Finalizers = common.RemoveFinalizer(suiteCopy.ObjectMeta.Finalizers, compv1alpha1.SuiteFinalizer)
	if err := r.client.Update(context.TODO(), suiteCopy); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileComplianceSuite) validateSuite(suite *compv1alpha1.ComplianceSuite) (bool, string) {
	if isValid, errorMsg := r.validateSchedule(suite); !isValid {
		return isValid, errorMsg
	}
	return true, ""
}

func (r *ReconcileComplianceSuite) issueValidationError(suite *compv1alpha1.ComplianceSuite, errorMsg string, logger logr.Logger) error {
	enhancedMessage := fmt.Sprintf("Suite was invalid: %s", errorMsg)
	logger.Info(enhancedMessage)

	// Issue event
	r.recorder.Event(
		suite,
		corev1.EventTypeWarning,
		"SuiteValidationError",
		errorMsg,
	)

	// Update status
	suiteCopy := suite.DeepCopy()
	suiteCopy.Status.AggregatedPhase = compv1alpha1.PhaseDone
	suiteCopy.Status.AggregatedResult = compv1alpha1.ResultError
	suiteCopy.Status.ErrorMessage = enhancedMessage
	if suiteCopy.Status.ScanStatuses == nil {
		suiteCopy.Status.ScanStatuses = make([]compv1alpha1.ComplianceScanStatusWrapper, 0)
	}
	return r.client.Status().Update(context.TODO(), suiteCopy)
}

func (r *ReconcileComplianceSuite) reconcileScans(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	for _, scanWrap := range suite.Spec.Scans {
		scan := &compv1alpha1.ComplianceScan{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: scanWrap.Name, Namespace: suite.Namespace}, scan)
		if err != nil && errors.IsNotFound(err) {
			// If the scan was not found, launch it
			logger.Info("Scan not found, launching..", "ComplianceScan.Name", scanWrap.Name)
			if err = launchScanForSuite(r, suite, &scanWrap, logger); err != nil {
				return err
			}
			logger.Info("Scan created", "ComplianceScan.Name", scanWrap.Name)
			// No point in reconciling status yet
			continue
		} else if err != nil {
			logger.Error(err, "Cannot get the scan for a suite", "ComplianceScan.Name", scanWrap.Name)
			return err
		}

		// The scan already exists, let's just make sure its status is reflected
		if err := r.reconcileScanStatus(suite, scan, logger); err != nil {
			return err
		}
	}

	return nil
}

func (r *ReconcileComplianceSuite) reconcileScanStatus(suite *compv1alpha1.ComplianceSuite, scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	// See if we already have a ScanStatusWrapper for this name
	for idx, scanStatusWrap := range suite.Status.ScanStatuses {
		if scan.Name == scanStatusWrap.Name {
			err := r.updateScanStatus(suite, idx, &scanStatusWrap, scan, logger)
			if err != nil {
				logger.Error(err, "Could not update scan status")
				return err
			}
			if r.recorder != nil && suite.IsResultAvailable() {
				r.generateEventsForSuite(suite, logger)
			}
			return nil
		}
	}

	err := r.addScanStatus(suite, scan, logger)
	if err != nil {
		logger.Error(err, "Could not add scan status")
		return err
	}

	return nil
}

// updates the status of a scan in the compliance suite. Note that the suite that this takes is already a copy, so it's safe to modify
func (r *ReconcileComplianceSuite) updateScanStatus(suite *compv1alpha1.ComplianceSuite, idx int, scanStatusWrap *compv1alpha1.ComplianceScanStatusWrapper, scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	// if yes, update it, if the status differs
	if scanStatusWrap.Phase == scan.Status.Phase {
		logger.Info("Not updating scan, the phase is the same", "ComplianceScan.Name", scanStatusWrap.Name, "ComplianceScan.Phase", scanStatusWrap.Phase)
		return nil
	}
	modScanStatus := compv1alpha1.ScanStatusWrapperFromScan(scan)

	// Replace the copy so we use fresh metadata
	suite = suite.DeepCopy()
	suite.Status.ScanStatuses[idx] = modScanStatus
	suite.Status.AggregatedPhase = suite.LowestCommonState()
	suite.Status.AggregatedResult = suite.LowestCommonResult()

	if suite.Status.AggregatedResult == compv1alpha1.ResultNotApplicable {
		suite.Status.ErrorMessage = "The suite result is not applicable, please check if you're using the correct platform"
	} else if suite.Status.AggregatedResult == compv1alpha1.ResultInconsistent {
		suite.Status.ErrorMessage = fmt.Sprintf("results were not consistent, search for compliancecheckresults labeled with %s",
			compv1alpha1.ComplianceCheckInconsistentLabel)
	}

	logger.Info("Updating scan status", "ComplianceScan.Name", modScanStatus.Name, "ComplianceScan.Phase", modScanStatus.Phase)
	return r.client.Status().Update(context.TODO(), suite)
}

func (r *ReconcileComplianceSuite) generateEventsForSuite(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) {
	logger.Info("Generating events for suite")

	// Event for Suite
	r.recorder.Event(
		suite,
		corev1.EventTypeNormal,
		"ResultAvailable",
		fmt.Sprintf("ComplianceSuite's result is: %s", suite.Status.AggregatedResult),
	)

	if suite.Status.AggregatedResult == compv1alpha1.ResultNotApplicable {
		r.recorder.Eventf(
			suite, corev1.EventTypeNormal, "SuiteNotApplicable",
			"The suite result is not applicable, please check if you're using the correct platform")
	} else if suite.Status.AggregatedResult == compv1alpha1.ResultInconsistent {
		r.recorder.Eventf(
			suite, corev1.EventTypeNormal, "SuiteNotConsistent",
			"The suite result is not consistent, please check for scan results labeled with %s",
			compv1alpha1.ComplianceCheckInconsistentLabel)
	}

	ownerRefs := suite.GetOwnerReferences()
	if len(ownerRefs) == 0 {
		return //there is nothing to do, since no owner is set
	}
	for _, ownerRef := range ownerRefs {
		// we are making an assumption that the GRC policy has a single owner, or we chose the first owner in the list
		if string(ownerRef.UID) == "" {
			continue //there is nothing to do, since no owner UID is set
		}
		// FIXME(jaosorior): Figure out a less hacky way to check this
		if ownerRef.Kind == "Policy" {
			pol := getParentPolicy(&ownerRef, suite.GetNamespace())
			r.recorder.Event(
				pol,
				corev1.EventTypeNormal,
				fmt.Sprintf("policy: %s/%s", suite.Namespace, suite.Name),
				resultToACMPolicyStatus(suite),
			)
		}
	}
}

func (r *ReconcileComplianceSuite) addScanStatus(suite *compv1alpha1.ComplianceSuite, scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	// if not, create the scan status with the name and the current state
	newScanStatus := compv1alpha1.ScanStatusWrapperFromScan(scan)

	// Replace the copy so we use fresh metadata
	suite = suite.DeepCopy()
	suite.Status.ScanStatuses = append(suite.Status.ScanStatuses, newScanStatus)
	logger.Info("Adding scan status", "ComplianceScan.Name", newScanStatus.Name, "ComplianceScan.Phase", newScanStatus.Phase)
	suite.Status.AggregatedPhase = suite.LowestCommonState()
	suite.Status.AggregatedResult = suite.LowestCommonResult()
	return r.client.Status().Update(context.TODO(), suite)
}

func launchScanForSuite(r *ReconcileComplianceSuite, suite *compv1alpha1.ComplianceSuite, scanWrap *compv1alpha1.ComplianceScanSpecWrapper, logger logr.Logger) error {
	scan := newScanForSuite(suite, scanWrap)
	if scan == nil {
		return fmt.Errorf("cannot create ComplianceScan for %s:%s", suite.Name, scanWrap.Name)
	}

	if err := controllerutil.SetControllerReference(suite, scan, r.scheme); err != nil {
		log.Error(err, "Failed to set scan ownership", "ComplianceScan.Name", scan.Name)
		return err
	}

	err := r.client.Create(context.TODO(), scan)
	if errors.IsAlreadyExists(err) {
		// Was there a race that created the scan in the meantime?
		return nil
	} else if err != nil {
		log.Error(err, "Failed to launch scan", "ComplianceScan.Name", scan.Name)
		return err
	}

	return nil
}

func newScanForSuite(suite *compv1alpha1.ComplianceSuite, scanWrap *compv1alpha1.ComplianceScanSpecWrapper) *compv1alpha1.ComplianceScan {
	scan := compv1alpha1.ComplianceScanFromWrapper(scanWrap)
	scan.SetLabels(map[string]string{
		compv1alpha1.SuiteLabel: suite.Name,
	})
	scan.SetNamespace(suite.Namespace)
	return scan
}

// Reconcile the remediation application in the suite. Note that the suite that this takes is already
// a copy, so it's safe to modify.
func (r *ReconcileComplianceSuite) reconcileRemediations(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) (reconcile.Result, error) {
	// We don't need to do anything else unless we gotta enabled auto-apply
	if !suite.Spec.AutoApplyRemediations {
		return reconcile.Result{}, nil
	}

	// Get all the remediations
	var remList compv1alpha1.ComplianceRemediationList
	mcfgpools := &mcfgv1.MachineConfigPoolList{}
	affectedMcfgPools := map[string]*mcfgv1.MachineConfigPool{}
	listOpts := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{compv1alpha1.SuiteLabel: suite.Name}),
	}

	if err := r.client.List(context.TODO(), &remList, &listOpts); err != nil {
		log.Error(err, "Failed to list remediations")
		return reconcile.Result{}, err
	}

	if err := r.client.List(context.TODO(), mcfgpools); err != nil {
		log.Error(err, "Failed to list pools")
		return reconcile.Result{}, err
	}

	// Construct the list of the statuses
	for _, rem := range remList.Items {
		if suite.Spec.AutoApplyRemediations {
			// get relevant scan
			scan := &compv1alpha1.ComplianceScan{}
			scanKey := types.NamespacedName{Name: rem.Labels[compv1alpha1.ScanLabel], Namespace: rem.Namespace}
			if err := r.client.Get(context.TODO(), scanKey, scan); err != nil {
				return reconcile.Result{}, err
			}
			if utils.IsMachineConfig(rem.Spec.Object) {
				// get affected pool
				pool, affectedPoolExists := r.getAffectedMcfgPool(scan, mcfgpools)
				// we only ned to operate on pools that are affected
				if affectedPoolExists {
					foundPool, poolIsTracked := affectedMcfgPools[pool.Name]
					if !poolIsTracked {
						foundPool = pool.DeepCopy()
						affectedMcfgPools[pool.Name] = foundPool
					}
					// Only apply remediations once the scan is done. This hopefully ensures
					// that we already have all the relevant remediations in place.
					// We only care for remediations that haven't been applied
					if scan.Status.Phase == compv1alpha1.PhaseDone &&
						rem.Status.ApplicationState != compv1alpha1.RemediationApplied {
						if err := r.applyMcfgRemediationAndPausePool(rem, foundPool, logger); err != nil {
							return reconcile.Result{}, err
						}
					}
				}
			} else {
				// Only apply remediations once the scan is done. This hopefully ensures
				// that we already have all the relevant remediations in place.
				// We only care for remediations that haven't been applied
				if scan.Status.Phase == compv1alpha1.PhaseDone &&
					rem.Status.ApplicationState != compv1alpha1.RemediationApplied {
					if err := r.applyGenericRemediation(rem, logger); err != nil {
						return reconcile.Result{}, err
					}
				}
			}
		}
	}

	// We only post-process when everything is done. This avoids unnecessary
	// "unpauses" for MachineConfigPools
	if suite.Status.AggregatedPhase != compv1alpha1.PhaseDone {
		logger.Info("Waiting until all scans are in Done phase before post-procesing remediations")
		return reconcile.Result{}, nil
	}

	// refresh remediationList
	if err := r.client.List(context.TODO(), &remList, &listOpts); err != nil {
		return reconcile.Result{}, err
	}

	// Check that all remediations have been applied yet. If not, cause an error
	// and requeue.
	for _, rem := range remList.Items {
		if rem.Status.ApplicationState != compv1alpha1.RemediationApplied {
			logger.Info("Remediation not applied yet. Skipping post-processing", "ComplianceRemediation.Name", rem.Name)
			return reconcile.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
		}
	}

	// Only un-pause MachineConfigPools once the remediations have been applied
	for _, pool := range affectedMcfgPools {
		if pool.Spec.Paused {
			logger.Info("Unpausing pool", "MachineConfigPool.Name", pool.Name)
			poolCopy := pool.DeepCopy()
			poolCopy.Spec.Paused = false
			if err := r.client.Update(context.TODO(), poolCopy); err != nil {
				logger.Error(err, "Could not unpause pool", "MachineConfigPool.Name", pool.Name)
				return reconcile.Result{}, err
			}
		}
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceSuite) applyGenericRemediation(rem compv1alpha1.ComplianceRemediation,
	logger logr.Logger) error {
	remCopy := rem.DeepCopy()
	remCopy.Spec.Apply = true
	logger.Info("Setting Remediation to applied", "ComplianceRemediation.Name", rem.Name)
	if err := r.client.Update(context.TODO(), remCopy); err != nil {
		return err
	}
	return nil
}

// This gets the remediation to be applied. Note that before being able to do that, the machineConfigPool is
// paused in order to reduce restarts of nodes.
func (r *ReconcileComplianceSuite) applyMcfgRemediationAndPausePool(rem compv1alpha1.ComplianceRemediation,
	pool *mcfgv1.MachineConfigPool, logger logr.Logger) error {
	remCopy := rem.DeepCopy()
	// Only pause pools where the pool wasn't paused before and
	// the remediation hasn't been applied
	if !pool.Spec.Paused {
		logger.Info("Pausing pool", "MachineConfigPool.Name", pool.Name)
		pool.Spec.Paused = true
		if err := r.client.Update(context.TODO(), pool); err != nil {
			logger.Error(err, "Could not pause pool", "MachineConfigPool.Name", pool.Name)
			return err
		}
	}

	remCopy.Spec.Apply = true
	if err := r.client.Update(context.TODO(), remCopy); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileComplianceSuite) getAffectedMcfgPool(scan *compv1alpha1.ComplianceScan, mcfgpools *mcfgv1.MachineConfigPoolList) (mcfgv1.MachineConfigPool, bool) {
	for _, pool := range mcfgpools.Items {
		if utils.McfgPoolLabelMatches(scan.Spec.NodeSelector, &pool) {
			return pool, true
		}
	}
	return mcfgv1.MachineConfigPool{}, false
}

func getParentPolicy(ownerRef *metav1.OwnerReference, ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": ownerRef.APIVersion,
			"kind":       ownerRef.Kind,
			"metadata": map[string]interface{}{
				"name":      ownerRef.Name,
				"namespace": ns,
				"uid":       ownerRef.UID,
			},
		},
	}
}

// Converts the given result of a ComplianceSuite into a string that's usable by ACM
func resultToACMPolicyStatus(suite *compv1alpha1.ComplianceSuite) string {
	const instfmt string = "; To view aggregated results, execute the following in the managed cluster: kubectl get compliancesuites -n %s %s"
	instructions := fmt.Sprintf(instfmt, suite.Namespace, suite.Name)
	var result string
	switch suite.Status.AggregatedResult {
	case compv1alpha1.ResultCompliant:
		result = "Compliant"
	case compv1alpha1.ResultNonCompliant:
		result = "NonCompliant"
	default:
		result = "UnknownCompliancy"
	}
	return result + instructions
}
