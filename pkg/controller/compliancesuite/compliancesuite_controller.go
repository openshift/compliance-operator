package compliancesuite

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/openshift/compliance-operator/pkg/controller/metrics"
	"github.com/openshift/compliance-operator/pkg/utils"
)

var log = logf.Log.WithName("suitectrl")

const (
	// The default time we should wait before requeuing
	requeueAfterDefault = 10 * time.Second
)

// Add creates a new ComplianceSuite Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, met *metrics.Metrics, si utils.CtlplaneSchedulingInfo) error {
	return add(mgr, newReconciler(mgr, met, si))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, met *metrics.Metrics, si utils.CtlplaneSchedulingInfo) reconcile.Reconciler {
	return &ReconcileComplianceSuite{
		reader:         mgr.GetAPIReader(),
		client:         mgr.GetClient(),
		scheme:         mgr.GetScheme(),
		recorder:       mgr.GetEventRecorderFor("suitectrl"),
		metrics:        met,
		schedulingInfo: si,
	}
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
	// Accesses the API server directly
	reader client.Reader
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
	metrics  *metrics.Metrics
	// helps us schedule platform scans on the nodes labeled for the
	// compliance operator's control plane
	schedulingInfo utils.CtlplaneSchedulingInfo
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

	// We only update the status to pending if there isn't a result already.
	if suite.Status.Phase == "" || (suite.Status.Conditions.GetCondition("Ready") == nil && !suite.IsResultAvailable()) {
		sCopy := suite.DeepCopy()
		sCopy.Status.Phase = compv1alpha1.PhasePending
		sCopy.Status.SetConditionPending()
		// ScanStatuses was initially marked as required. We create it here
		// for cases whent he CRD is cached.
		if sCopy.Status.ScanStatuses == nil {
			sCopy.Status.ScanStatuses = make([]compv1alpha1.ComplianceScanStatusWrapper, 0)
		}
		updateErr := r.client.Status().Update(context.TODO(), sCopy)
		if updateErr != nil {
			return reconcile.Result{}, fmt.Errorf("Error setting initial status for suite: %w", updateErr)
		}
		return reconcile.Result{}, nil
	}

	if isValid, errorMsg := r.validateSuite(suite); !isValid {
		// return immediately and don't schedule nor reconcile scans
		return reconcile.Result{}, r.issueValidationError(suite, errorMsg, reqLogger)
	}

	if suite.Status.Conditions.GetCondition("Processing") == nil {
		sCopy := suite.DeepCopy()
		sCopy.Status.SetConditionsProcessing()
		updateErr := r.client.Status().Update(context.TODO(), sCopy)
		if updateErr != nil {
			return reconcile.Result{}, fmt.Errorf("Error setting processing status for suite: %w", updateErr)
		}
		return reconcile.Result{}, nil
	}

	suiteCopy := suite.DeepCopy()
	rescheduleWithDelay, err := r.reconcileScans(suiteCopy, reqLogger)
	if err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	} else if rescheduleWithDelay {
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, err
	}

	var res reconcile.Result
	if res, err = r.reconcileRemediations(suiteCopy, reqLogger); err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	if suiteCopy.IsResultAvailable() {
		sCopy := suite.DeepCopy()
		sCopy.Status.SetConditionReady()
		updateErr := r.client.Status().Update(context.TODO(), sCopy)
		if updateErr != nil {
			return reconcile.Result{}, fmt.Errorf("Error setting ready status for suite: %w", updateErr)
		}
		return res, r.reconcileScanRerunnerCronJob(suiteCopy, reqLogger)
	}

	return res, nil
}

func (r *ReconcileComplianceSuite) suiteDeleteHandler(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	rerunner := r.getRerunner(suite)
	if err := r.handleRerunnerDelete(rerunner, suite.Name, logger); err != nil {
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
	suiteCopy.Status.Phase = compv1alpha1.PhaseDone
	suiteCopy.Status.Result = compv1alpha1.ResultError
	suiteCopy.Status.ErrorMessage = enhancedMessage
	suiteCopy.Status.SetConditionInvalid()
	if suiteCopy.Status.ScanStatuses == nil {
		suiteCopy.Status.ScanStatuses = make([]compv1alpha1.ComplianceScanStatusWrapper, 0)
	}
	return r.client.Status().Update(context.TODO(), suiteCopy)
}

func (r *ReconcileComplianceSuite) reconcileScans(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) (bool, error) {
	for idx := range suite.Spec.Scans {
		scanWrap := &suite.Spec.Scans[idx]
		scan := &compv1alpha1.ComplianceScan{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: scanWrap.Name, Namespace: suite.Namespace}, scan)
		if err != nil && errors.IsNotFound(err) {
			// If the scan was not found, launch it
			logger.Info("Scan not found, launching..", "ComplianceScan.Name", scanWrap.Name)
			if err = launchScanForSuite(r, suite, scanWrap, logger); err != nil {
				return false, err
			}
			logger.Info("Scan created", "ComplianceScan.Name", scanWrap.Name)
			// No point in reconciling status yet
			continue
		} else if err != nil {
			logger.Error(err, "Cannot get the scan for a suite", "ComplianceScan.Name", scanWrap.Name)
			return false, err
		}

		// The scan already exists and is up to date, let's just make sure its status is reflected
		if err := r.reconcileScanStatus(suite, scan, logger); err != nil {
			return false, err
		}

		// Update the scan spec (last becuase it's a corner case)
		rescheduleWithDelay, err := r.reconcileScanSpec(scanWrap, scan, logger)
		if rescheduleWithDelay || err != nil {
			return rescheduleWithDelay, err
		}

		// FIXME: delete scans that went away
	}

	return false, nil
}

func (r *ReconcileComplianceSuite) reconcileScanStatus(suite *compv1alpha1.ComplianceSuite, scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	// See if we already have a ScanStatusWrapper for this name
	for idx := range suite.Status.ScanStatuses {
		scanStatusWrap := suite.Status.ScanStatuses[idx]
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

func (r *ReconcileComplianceSuite) reconcileScanSpec(scanWrap *compv1alpha1.ComplianceScanSpecWrapper, origScan *compv1alpha1.ComplianceScan, logger logr.Logger) (bool, error) {
	// Do we need to update the scan?
	if scanWrap.ScanSpecDiffers(origScan) == false {
		logger.Info("Scan is up to date", "ComplianceScan.Name", origScan.Name)
		return false, nil
	}

	// Fetch the scan again in case the status is updated. Updating the scan spec
	// is so rare that the extra API server round-trip shouldn't matter
	foundScan := &compv1alpha1.ComplianceScan{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: origScan.Name, Namespace: origScan.Namespace}, foundScan)
	if err != nil {
		return false, err
	}

	// We only update scans that are in DONE, because otherwise we might end up
	// with inconsistencies (scan pods running where they shouldn't run, checks
	// or remediations might be already created for the old content etc...)
	if foundScan.Status.Phase != compv1alpha1.PhaseDone {
		logger.Info("Refusing to update a scan that is not done, retrying later")
		return true, nil
	}

	scanWrap.ComplianceScanSpec.DeepCopyInto(&foundScan.Spec)
	err = r.client.Update(context.TODO(), foundScan)
	if err != nil {
		logger.Error(err, "Cannot update scan spec", "ComplianceScan.Name", origScan.Name)
		return false, err
	}
	logger.Info("Scan updated", "ComplianceScan.Name", origScan.Name)
	return false, nil
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
	suite.Status.Phase = suite.LowestCommonState()
	suite.Status.Result = suite.LowestCommonResult()

	if suite.Status.Result == compv1alpha1.ResultNotApplicable {
		suite.Status.ErrorMessage = "The suite result is not applicable, please check if you're using the correct platform"
	} else if suite.Status.Result == compv1alpha1.ResultInconsistent {
		suite.Status.ErrorMessage = fmt.Sprintf("results were not consistent, search for compliancecheckresults labeled with %s",
			compv1alpha1.ComplianceCheckInconsistentLabel)
	} else {
		suite.Status.SetConditionsProcessing()
	}

	logger.Info("Updating scan status", "ComplianceScan.Name", modScanStatus.Name, "ComplianceScan.Phase", modScanStatus.Phase)
	if err := r.client.Status().Update(context.TODO(), suite); err != nil {
		return err
	}
	return r.setSuiteMetric(suite)
}

func (r *ReconcileComplianceSuite) generateEventsForSuite(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) {
	logger.Info("Generating events for suite")

	if suite.Status.Result == compv1alpha1.ResultNotApplicable {
		r.recorder.Eventf(
			suite, corev1.EventTypeNormal, "SuiteNotApplicable",
			"The suite result is not applicable, please check if you're using the correct platform")
	} else if suite.Status.Result == compv1alpha1.ResultInconsistent {
		r.recorder.Eventf(
			suite, corev1.EventTypeNormal, "SuiteNotConsistent",
			"The suite result is not consistent, please check for scan results labeled with %s",
			compv1alpha1.ComplianceCheckInconsistentLabel)
	}

	err, haveOutdatedRems := utils.HaveOutdatedRemediations(r.client)
	if err != nil {
		logger.Info("Could not check if there exist any obsolete remediations", "Suite.Name", suite.Name)
	}
	if haveOutdatedRems {
		r.recorder.Eventf(
			suite, corev1.EventTypeNormal, "HaveOutdatedRemediations",
			"One of suite's scans produced outdated remediations, please check for complianceremediation objects labeled with %s",
			compv1alpha1.OutdatedRemediationLabel)
	}
	common.GenerateEventForResult(r.recorder, suite, suite, suite.Status.Result)
}

func (r *ReconcileComplianceSuite) addScanStatus(suite *compv1alpha1.ComplianceSuite, scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	// if not, create the scan status with the name and the current state
	newScanStatus := compv1alpha1.ScanStatusWrapperFromScan(scan)

	// Replace the copy so we use fresh metadata
	suite = suite.DeepCopy()
	suite.Status.ScanStatuses = append(suite.Status.ScanStatuses, newScanStatus)
	logger.Info("Adding scan status", "ComplianceScan.Name", newScanStatus.Name, "ComplianceScan.Phase", newScanStatus.Phase)
	suite.Status.Phase = suite.LowestCommonState()
	suite.Status.Result = suite.LowestCommonResult()
	if err := r.client.Status().Update(context.TODO(), suite); err != nil {
		return err
	}
	return r.setSuiteMetric(suite)
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
	// We don't need to do anything else unless auto-applied is enabled
	if !suite.ShouldApplyRemediations() {
		return reconcile.Result{}, nil
	}

	// Get all the remediations
	remList := &compv1alpha1.ComplianceRemediationList{}
	mcfgpools := &mcfgv1.MachineConfigPoolList{}
	affectedMcfgPools := map[string]*mcfgv1.MachineConfigPool{}
	listOpts := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{compv1alpha1.SuiteLabel: suite.Name}),
	}

	if err := r.client.List(context.TODO(), remList, &listOpts); err != nil {
		log.Error(err, "Failed to list remediations")
		return reconcile.Result{}, err
	}

	if err := r.client.List(context.TODO(), mcfgpools); err != nil {
		log.Error(err, "Failed to list pools")
		return reconcile.Result{}, err
	}

	// Construct the list of the statuses
	for _, rem := range remList.Items {
		// get relevant scan
		scan := &compv1alpha1.ComplianceScan{}
		scanKey := types.NamespacedName{Name: rem.Labels[compv1alpha1.ComplianceScanLabel], Namespace: rem.Namespace}
		if err := r.client.Get(context.TODO(), scanKey, scan); err != nil {
			return reconcile.Result{}, err
		}

		// Only apply remediations once the scan is done. This hopefully ensures
		// that we already have all the relevant remediations in place.
		// We only care for remediations that haven't been applied
		if scan.Status.Phase != compv1alpha1.PhaseDone {
			continue
		}

		if err := r.applyRemediation(rem, suite, scan, mcfgpools, affectedMcfgPools, logger); err != nil {
			return reconcile.Result{}, err
		}
	}

	// We only post-process when everything is done. This avoids unnecessary
	// "unpauses" for MachineConfigPools
	if suite.Status.Phase != compv1alpha1.PhaseDone {
		logger.Info("Waiting until all scans are in Done phase before post-procesing remediations")
		return reconcile.Result{}, nil
	}

	logger.Info("All scans are in Done phase. Post-processing remediations")
	// refresh remediationList
	postProcessRemList := &compv1alpha1.ComplianceRemediationList{}
	if err := r.client.List(context.TODO(), postProcessRemList, &listOpts); err != nil {
		return reconcile.Result{}, err
	}

	// Check that all remediations have been applied yet. If not, requeue.
	for _, rem := range postProcessRemList.Items {
		if !rem.IsApplied() {
			if rem.Status.ApplicationState == compv1alpha1.RemediationNeedsReview {
				r.recorder.Event(suite, corev1.EventTypeWarning, "CannotRemediate", "Remediation needs-review. Values not set"+" Remediation:"+rem.Name)
				continue
			}
			logger.Info("Remediation not applied yet. Skipping post-processing", "ComplianceRemediation.Name", rem.Name)
			return reconcile.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
		}
	}

	// Only un-pause MachineConfigPools once the remediations have been applied
	for idx := range affectedMcfgPools {
		pool := affectedMcfgPools[idx]
		// only un-pause if the kubeletconfig is fully rendered for the pool
		isRendered, err, diffString := utils.AreKubeletConfigsRendered(pool, r.client)
		if err != nil {
			return reconcile.Result{}, err
		}
		if !isRendered {
			logger.Info("Waiting until all kubeletconfigs are rendered before un-pausing", "MachineConfigPool.Name", pool.Name)
			logger.Info("KubeletConfig render diff:", "MachineConfigPool.Name", pool.Name, "Diff", diffString)
			return reconcile.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
		}
		poolKey := types.NamespacedName{Name: pool.GetName()}
		// refresh pool reference directly from the API Server
		if getErr := r.reader.Get(context.TODO(), poolKey, pool); getErr != nil {
			logger.Error(getErr, "Could get newer machine config pool reference", "MachineConfigPool.Name", poolKey.Name)
			return reconcile.Result{}, getErr
		}
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

	if suite.ApplyRemediationsAnnotationSet() || suite.RemoveOutdatedAnnotationSet() {
		suiteCopy := suite.DeepCopy()
		if suite.ApplyRemediationsAnnotationSet() {
			delete(suiteCopy.Annotations, compv1alpha1.ApplyRemediationsAnnotation)
		}
		if suite.RemoveOutdatedAnnotationSet() {
			delete(suiteCopy.Annotations, compv1alpha1.RemoveOutdatedAnnotation)
		}
		updateErr := r.client.Update(context.TODO(), suiteCopy)
		return reconcile.Result{}, updateErr
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceSuite) applyRemediation(rem compv1alpha1.ComplianceRemediation,
	suite *compv1alpha1.ComplianceSuite,
	scan *compv1alpha1.ComplianceScan,
	mcfgpools *mcfgv1.MachineConfigPoolList,
	affectedMcfgPools map[string]*mcfgv1.MachineConfigPool,
	logger logr.Logger) error {
	if utils.IsMachineConfig(rem.Spec.Current.Object) || utils.IsKubeletConfig(rem.Spec.Current.Object) {
		// get affected pool
		pool := r.getAffectedMcfgPool(scan, mcfgpools)
		// we only need to operate on pools that are affected
		if pool != nil {
			foundPool, poolIsTracked := affectedMcfgPools[pool.Name]
			if !poolIsTracked {
				foundPool = pool.DeepCopy()
				affectedMcfgPools[pool.Name] = foundPool
			}
			// we will use the same logic here for Kubelet Config remediation
			if err := r.applyMcfgRemediationAndPausePool(rem, suite, foundPool, logger); err != nil {
				return err
			}
		}

		return nil
	}

	if err := r.applyGenericRemediation(rem, suite, logger); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileComplianceSuite) applyGenericRemediation(rem compv1alpha1.ComplianceRemediation,
	suite *compv1alpha1.ComplianceSuite,
	logger logr.Logger) error {
	remCopy := rem.DeepCopy()
	remCopy.Spec.Apply = true
	if remediationNeedsOutdatedRemoval(remCopy, suite) {
		logger.Info("Updating Outdated Remediation", "Remediation.Name", remCopy.Name)
		remCopy.Spec.Outdated.Object = nil
	}
	logger.Info("Setting Remediation to applied", "ComplianceRemediation.Name", rem.Name)
	if err := r.client.Update(context.TODO(), remCopy); err != nil {
		return err
	}
	return nil
}

// This gets the remediation to be applied. Note that before being able to do that, the machineConfigPool is
// paused in order to reduce restarts of nodes.
func (r *ReconcileComplianceSuite) applyMcfgRemediationAndPausePool(rem compv1alpha1.ComplianceRemediation,
	suite *compv1alpha1.ComplianceSuite,
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
	if remediationNeedsOutdatedRemoval(remCopy, suite) {
		logger.Info("Updating Outdated Remediation", "Remediation.Name", remCopy.Name)
		remCopy.Spec.Outdated.Object = nil
	}
	if err := r.client.Update(context.TODO(), remCopy); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileComplianceSuite) getAffectedMcfgPool(scan *compv1alpha1.ComplianceScan, mcfgpools *mcfgv1.MachineConfigPoolList) *mcfgv1.MachineConfigPool {
	for i := range mcfgpools.Items {
		pool := &mcfgpools.Items[i]
		if utils.McfgPoolLabelMatches(scan.Spec.NodeSelector, pool) {
			return pool
		}
	}
	return nil
}

func remediationNeedsOutdatedRemoval(rem *compv1alpha1.ComplianceRemediation, suite *compv1alpha1.ComplianceSuite) bool {
	return suite.ShouldRemoveOutdated() && rem.Status.ApplicationState == compv1alpha1.RemediationOutdated
}

func (r *ReconcileComplianceSuite) setSuiteMetric(suite *compv1alpha1.ComplianceSuite) error {
	if suite.Status.Result == compv1alpha1.ResultCompliant {
		r.metrics.SetComplianceStateInCompliance(suite.Name)
	} else if suite.Status.Result == compv1alpha1.ResultNonCompliant {
		r.metrics.SetComplianceStateOutOfCompliance(suite.Name)
	} else if suite.Status.Result == compv1alpha1.ResultInconsistent {
		r.metrics.SetComplianceStateInconsistent(suite.Name)
	} else if suite.Status.Result == compv1alpha1.ResultError {
		r.metrics.SetComplianceStateError(suite.Name)
	}
	return nil
}
