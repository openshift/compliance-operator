package compliancescan

import (
	"context"
	goerrors "errors"
	"fmt"
	"math"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/controller/metrics"
	"github.com/openshift/compliance-operator/pkg/utils"
)

var log = logf.Log.WithName("scanctrl")

var oneReplica int32 = 1

var (
	trueVal     = true
	hostPathDir = corev1.HostPathDirectory
)

const (
	// flag that indicates that deletion should be done
	doDelete = true
	// flag that indicates that no deletion should take place
	dontDelete = false
)

const (
	// OpenSCAPScanContainerName defines the name of the contianer that will run OpenSCAP
	OpenSCAPScanContainerName = "scanner"
	// The default time we should wait before requeuing
	requeueAfterDefault = 10 * time.Second
)

// Add creates a new ComplianceScan Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, met *metrics.Metrics, si utils.CtlplaneSchedulingInfo) error {
	return add(mgr, newReconciler(mgr, met, si))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, met *metrics.Metrics, si utils.CtlplaneSchedulingInfo) reconcile.Reconciler {
	return &ReconcileComplianceScan{
		client:         mgr.GetClient(),
		scheme:         mgr.GetScheme(),
		recorder:       mgr.GetEventRecorderFor("scanctrl"),
		metrics:        met,
		schedulingInfo: si,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("compliancescan-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource ComplianceScan
	err = c.Watch(&source.Kind{Type: &compv1alpha1.ComplianceScan{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileComplianceScan implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileComplianceScan{}

// ReconcileComplianceScan reconciles a ComplianceScan object
type ReconcileComplianceScan struct {
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

// Reconcile reads that state of the cluster for a ComplianceScan object and makes changes based on the state read
// and what is in the ComplianceScan.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileComplianceScan) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling ComplianceScan")

	// Fetch the ComplianceScan instance
	instance := &compv1alpha1.ComplianceScan{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !common.ContainsFinalizer(instance.ObjectMeta.Finalizers, compv1alpha1.ScanFinalizer) {
			instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, compv1alpha1.ScanFinalizer)
			if err := r.client.Update(context.TODO(), instance); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		return r.scanDeleteHandler(instance, reqLogger)
	}

	// At this point, we make a copy of the instance, so we can modify it in the functions below.
	scanToBeUpdated := instance.DeepCopy()
	if cont, err := r.validate(instance, reqLogger); !cont || err != nil {
		if err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	scanTypeHandler, err := getScanTypeHandler(r, scanToBeUpdated, reqLogger)
	if err != nil {
		return reconcile.Result{}, err
	}

	if cont, err := scanTypeHandler.validate(); !cont || err != nil {
		if err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	switch scanToBeUpdated.Status.Phase {
	case compv1alpha1.PhasePending:
		return r.phasePendingHandler(scanToBeUpdated, reqLogger)
	case compv1alpha1.PhaseLaunching:
		return r.phaseLaunchingHandler(scanTypeHandler, reqLogger)
	case compv1alpha1.PhaseRunning:
		return r.phaseRunningHandler(scanTypeHandler, reqLogger)
	case compv1alpha1.PhaseAggregating:
		return r.phaseAggregatingHandler(scanTypeHandler, reqLogger)
	case compv1alpha1.PhaseDone:
		return r.phaseDoneHandler(scanTypeHandler, scanToBeUpdated, reqLogger, dontDelete)
	}

	// the default catch-all, just remove the request from the queue
	return reconcile.Result{}, nil
}

// validate does validation on the scan and sets some defaults. This is run before any phase to avoid
// folks modifying the scan in the middle of it and the operator not erroring out early enough.
func (r *ReconcileComplianceScan) validate(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (cont bool, valerr error) {
	// If no phase set, default to pending (the initial phase):
	if instance.Status.Phase == "" {
		instanceCopy := instance.DeepCopy()
		instanceCopy.Status.Phase = compv1alpha1.PhasePending
		updateErr := r.client.Status().Update(context.TODO(), instanceCopy)
		if updateErr != nil {
			return false, updateErr
		}
		r.metrics.IncComplianceScanStatus(instanceCopy.Name, instanceCopy.Status)
		return false, nil
	}

	// Set default scan type if missing
	if instance.Spec.ScanType == "" {
		instanceCopy := instance.DeepCopy()
		instanceCopy.Spec.ScanType = compv1alpha1.ScanTypeNode
		err := r.client.Update(context.TODO(), instanceCopy)
		return false, err
	}

	// validate scan type
	if _, err := instance.GetScanTypeIfValid(); err != nil {
		r.recorder.Event(instance, corev1.EventTypeWarning, "InvalidScanType",
			"The scan type was invalid")
		instanceCopy := instance.DeepCopy()
		instanceCopy.Status.Result = compv1alpha1.ResultError
		instanceCopy.Status.ErrorMessage = fmt.Sprintf("Scan type '%s' is not valid", instance.Spec.ScanType)
		instanceCopy.Status.Phase = compv1alpha1.PhaseDone
		updateErr := r.client.Status().Update(context.TODO(), instanceCopy)
		if updateErr != nil {
			return false, updateErr
		}
		r.metrics.IncComplianceScanStatus(instanceCopy.Name, instanceCopy.Status)
		return false, nil
	}

	// Set default storage if missing
	if instance.Spec.RawResultStorage.Size == "" {
		instanceCopy := instance.DeepCopy()
		instanceCopy.Spec.RawResultStorage.Size = compv1alpha1.DefaultRawStorageSize
		err := r.client.Update(context.TODO(), instanceCopy)
		return false, err
	}

	if len(instance.Spec.RawResultStorage.PVAccessModes) == 0 {
		instanceCopy := instance.DeepCopy()
		instanceCopy.Spec.RawResultStorage.PVAccessModes = defaultAccessMode
		err := r.client.Update(context.TODO(), instanceCopy)
		return false, err
	}

	//validate raw storage size
	if _, err := resource.ParseQuantity(instance.Spec.RawResultStorage.Size); err != nil {
		instanceCopy := instance.DeepCopy()
		instanceCopy.Status.ErrorMessage = fmt.Sprintf("Error parsing RawResultsStorageSize: %s", err)
		instanceCopy.Status.Result = compv1alpha1.ResultError
		instanceCopy.Status.Phase = compv1alpha1.PhaseDone
		err := r.client.Status().Update(context.TODO(), instanceCopy)
		if err != nil {
			return false, err
		}
		r.metrics.IncComplianceScanStatus(instanceCopy.Name, instanceCopy.Status)
		return false, nil
	}

	return true, nil
}

func (r *ReconcileComplianceScan) phasePendingHandler(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Pending")

	// Remove annotation if needed
	if instance.NeedsRescan() {
		instanceCopy := instance.DeepCopy()
		delete(instanceCopy.Annotations, compv1alpha1.ComplianceScanRescanAnnotation)
		err := r.client.Update(context.TODO(), instanceCopy)
		return reconcile.Result{}, err
	}

	// Update the scan instance, the next phase is running
	instance.Status.Phase = compv1alpha1.PhaseLaunching
	instance.Status.Result = compv1alpha1.ResultNotAvailable
	err := r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		logger.Error(err, "Cannot update the status")
		return reconcile.Result{}, err
	}

	// TODO: It might be better to store the list of eligible nodes in the CR so that if someone edits the CR or
	// adds/removes nodes while the scan is running, we just work on the same set?

	r.metrics.IncComplianceScanStatus(instance.Name, instance.Status)
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseLaunchingHandler(h scanTypeHandler, logger logr.Logger) (reconcile.Result, error) {
	var err error

	logger.Info("Phase: Launching")

	scan := h.getScan()
	err = createConfigMaps(r, scriptCmForScan(scan), envCmForScan(scan), envCmForPlatformScan(scan), scan)
	if err != nil {
		logger.Error(err, "Cannot create the configmaps")
		return reconcile.Result{}, err
	}

	if err = r.handleRootCASecret(scan, logger); err != nil {
		logger.Error(err, "Cannot create CA secret")
		return reconcile.Result{}, err
	}

	if err = r.handleResultServerSecret(scan, logger); err != nil {
		logger.Error(err, "Cannot create result server cert secret")
		return reconcile.Result{}, err
	}

	if err = r.handleResultClientSecret(scan, logger); err != nil {
		logger.Error(err, "Cannot create result client cert secret")
		return reconcile.Result{}, err
	}

	if resume, err := r.handleRawResultsForScan(scan, logger); err != nil || !resume {
		if err != nil {
			logger.Error(err, "Cannot create the PersistentVolumeClaims")
		}
		return reconcile.Result{}, err
	}

	if err = r.createResultServer(scan, logger); err != nil {
		logger.Error(err, "Cannot create result server")
		return reconcile.Result{}, err
	}

	if err = h.createScanWorkload(); err != nil {
		if !common.IsRetriable(err) {
			// Surface non-retriable errors to the CR
			logger.Info("Updating scan status due to unretriable error")
			scanCopy := scan.DeepCopy()
			scanCopy.Status.ErrorMessage = err.Error()
			scanCopy.Status.Result = compv1alpha1.ResultError
			scanCopy.Status.Phase = compv1alpha1.PhaseDone
			if updateerr := r.client.Status().Update(context.TODO(), scanCopy); updateerr != nil {
				logger.Error(updateerr, "Failed to update a scan")
				return reconcile.Result{}, updateerr
			}
			r.metrics.IncComplianceScanStatus(scanCopy.Name, scanCopy.Status)
		}
		return common.ReturnWithRetriableError(logger, err)
	}

	// if we got here, there are no new pods to be created, move to the next phase
	scan.Status.Phase = compv1alpha1.PhaseRunning
	err = r.client.Status().Update(context.TODO(), scan)
	if err != nil {
		// metric status update error
		return reconcile.Result{}, err
	}
	r.metrics.IncComplianceScanStatus(scan.Name, scan.Status)
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseRunningHandler(h scanTypeHandler, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Running")

	running, err := h.handleRunningScan()

	if err != nil {
		return reconcile.Result{}, err
	}
	if running {
		// The platform scan pod is still running, go back to queue.
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	scan := h.getScan()
	// if we got here, there are no pods running, move to the Aggregating phase
	scan.Status.Phase = compv1alpha1.PhaseAggregating
	err = r.client.Status().Update(context.TODO(), scan)
	if err != nil {
		// metric status update error
		return reconcile.Result{}, err
	}
	r.metrics.IncComplianceScanStatus(scan.Name, scan.Status)
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseAggregatingHandler(h scanTypeHandler, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Aggregating")
	instance := h.getScan()
	isReady, warnings, err := h.shouldLaunchAggregator()

	if warnings != "" {
		instance.Status.Warnings = warnings
	}

	// We only wait if there are no errors.
	if err == nil && !isReady {
		logger.Info("ConfigMap missing (not ready). Requeuing.")
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	if err != nil {
		instance.Status.Phase = compv1alpha1.PhaseDone
		instance.Status.Result = compv1alpha1.ResultError
		instance.Status.ErrorMessage = err.Error()
		err = r.updateStatusWithEvent(instance, logger)
		if err != nil {
			// metric status update error
			return reconcile.Result{}, err
		}
		r.metrics.IncComplianceScanStatus(instance.Name, instance.Status)
		return reconcile.Result{}, nil
	}

	logger.Info("Creating an aggregator pod for scan")
	aggregator := r.newAggregatorPod(instance, logger)
	err = r.launchAggregatorPod(instance, aggregator, logger)
	if err != nil {
		logger.Error(err, "Failed to launch aggregator pod", "aggregator", aggregator)
		return reconcile.Result{}, err
	}

	running, err := isAggregatorRunning(r, instance, logger)
	if errors.IsNotFound(err) {
		// Suppress loud error message by requeueing
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault / 2}, nil
	} else if err != nil {
		logger.Error(err, "Failed to check if aggregator pod is running", "aggregator", aggregator)
		return reconcile.Result{}, err
	}

	if running {
		logger.Info("Remaining in the aggregating phase")
		instance.Status.Phase = compv1alpha1.PhaseAggregating
		err = r.client.Status().Update(context.TODO(), instance)
		if err != nil {
			logger.Error(err, "Cannot update the status, requeueing")
			return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
		}
		r.metrics.IncComplianceScanStatus(instance.Name, instance.Status)
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	logger.Info("Moving on to the Done phase")

	result, isReady, err := gatherResults(r, h)

	// We only wait if there are no errors.
	if err == nil && !isReady {
		logger.Info("ConfigMap missing or not ready. Requeuing.")
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	instance.Status.Result = result
	if err != nil {
		instance.Status.ErrorMessage = err.Error()
	}

	instance.Status.Phase = compv1alpha1.PhaseDone
	err = r.updateStatusWithEvent(instance, logger)
	if err != nil {
		// metric status update error
		return reconcile.Result{}, err
	}
	r.metrics.IncComplianceScanStatus(instance.Name, instance.Status)
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseDoneHandler(h scanTypeHandler, instance *compv1alpha1.ComplianceScan, logger logr.Logger, doDelete bool) (reconcile.Result, error) {
	var err error
	logger.Info("Phase: Done")

	// the scan pods and the aggregator are done at this point and can be cleaned up regardless
	// of the doDelete flag, unless we are running in debug mode and thus requested them to stay
	// around for later inspection
	if instance.Spec.Debug == false || instance.NeedsRescan() {
		// Don't try to clean up scan-type specific resources
		// if it was an unknown scan type
		if h != nil {
			if err := h.cleanup(); err != nil {
				logger.Error(err, "Cannot clean up scan pods")
				return reconcile.Result{}, err
			}
		}

		if err := r.deleteAggregator(instance, logger); err != nil {
			logger.Error(err, "Cannot delete aggregator")
			return reconcile.Result{}, err
		}
	}

	// We need to remove resources before doing a re-scan
	if doDelete || instance.NeedsRescan() {
		logger.Info("Cleaning up scan's resources")
		if err := r.deleteResultServer(instance, logger); err != nil {
			logger.Error(err, "Cannot delete result server")
			return reconcile.Result{}, err
		}

		if err = r.deleteResultServerSecret(instance, logger); err != nil {
			logger.Error(err, "Cannot delete result server cert secret")
			return reconcile.Result{}, err
		}

		if err = r.deleteResultClientSecret(instance, logger); err != nil {
			logger.Error(err, "Cannot delete result client cert secret")
			return reconcile.Result{}, err
		}

		if err = r.deleteRootCASecret(instance, logger); err != nil {
			logger.Error(err, "Cannot delete CA secret")
			return reconcile.Result{}, err
		}

		if err = r.deleteScriptConfigMaps(instance, logger); err != nil {
			logger.Error(err, "Cannot delete script ConfigMaps")
			return reconcile.Result{}, err
		}

		if instance.NeedsRescan() {
			if err = r.deleteResultConfigMaps(instance, logger); err != nil {
				logger.Error(err, "Cannot delete result ConfigMaps")
				return reconcile.Result{}, err
			}

			// reset phase
			logger.Info("Resetting scan")
			instanceCopy := instance.DeepCopy()
			instanceCopy.Status.Phase = compv1alpha1.PhasePending
			instanceCopy.Status.Result = compv1alpha1.ResultNotAvailable
			if instance.Status.CurrentIndex == math.MaxInt64 {
				instanceCopy.Status.CurrentIndex = 0
			} else {
				instanceCopy.Status.CurrentIndex = instance.Status.CurrentIndex + 1
			}
			err = r.client.Status().Update(context.TODO(), instanceCopy)
			if err != nil {
				// metric status update error
				return reconcile.Result{}, err
			}
			r.metrics.IncComplianceScanStatus(instanceCopy.Name, instanceCopy.Status)
			return reconcile.Result{}, nil
		}
	} else {
		// If we're done with the scan but we're not cleaning up just yet.

		// scale down resultserver so it's not still listening for requests.
		if err := r.scaleDownResultServer(instance, logger); err != nil {
			logger.Error(err, "Cannot scale down result server")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) scanDeleteHandler(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	if common.ContainsFinalizer(instance.ObjectMeta.Finalizers, compv1alpha1.ScanFinalizer) {
		logger.Info("The scan is being deleted")
		scanToBeDeleted := instance.DeepCopy()

		scanTypeHandler, err := getScanTypeHandler(r, scanToBeDeleted, logger)
		if err != nil && !goerrors.Is(err, compv1alpha1.ErrUnkownScanType) {
			return reconcile.Result{}, err
		}

		// Force remove rescan annotation since we're deleting the scan
		if scanToBeDeleted.NeedsRescan() {
			delete(scanToBeDeleted.Annotations, compv1alpha1.ComplianceScanRescanAnnotation)
		}

		// remove objects by forcing handling of phase DONE
		if _, err := r.phaseDoneHandler(scanTypeHandler, scanToBeDeleted, logger, doDelete); err != nil {
			// if fail to delete the external dependency here, return with error
			// so that it can be retried
			return reconcile.Result{}, err
		}

		if err := r.deleteResultConfigMaps(scanToBeDeleted, logger); err != nil {
			logger.Error(err, "Cannot delete result ConfigMaps")
			return reconcile.Result{}, err
		}

		if err := r.deleteRawResultsForScan(scanToBeDeleted); err != nil {
			logger.Error(err, "Cannot delete raw results")
			return reconcile.Result{}, err
		}

		// remove our finalizer from the list and update it.
		scanToBeDeleted.ObjectMeta.Finalizers = common.RemoveFinalizer(scanToBeDeleted.ObjectMeta.Finalizers, compv1alpha1.ScanFinalizer)
		if err := r.client.Update(context.TODO(), scanToBeDeleted); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Stop reconciliation as the item is being deleted
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) updateStatusWithEvent(scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	err := r.client.Status().Update(context.TODO(), scan)
	if err != nil {
		return err
	}
	if r.recorder != nil {
		r.generateResultEventForScan(scan, logger)
	}
	return nil
}

func (r *ReconcileComplianceScan) generateResultEventForScan(scan *compv1alpha1.ComplianceScan, logger logr.Logger) {
	logger.Info("Generating result event for scan")

	// Event for Suite
	r.recorder.Eventf(
		scan, corev1.EventTypeNormal, "ResultAvailable",
		"ComplianceScan's result is: %s", scan.Status.Result,
	)

	if scan.Status.Result == compv1alpha1.ResultNotApplicable {
		r.recorder.Eventf(
			scan, corev1.EventTypeWarning, "ScanNotApplicable",
			"The scan result is not applicable, please check if you're using the correct platform or if the nodeSelector matches nodes.")
	} else if scan.Status.Result == compv1alpha1.ResultInconsistent {
		r.recorder.Eventf(
			scan, corev1.EventTypeNormal, "ScanNotConsistent",
			"The scan result is not consistent, please check for scan results labeled with %s",
			compv1alpha1.ComplianceCheckInconsistentLabel)
	}

	err, haveOutdatedRems := utils.HaveOutdatedRemediations(r.client)
	if err != nil {
		logger.Info("Could not check if there exist any obsolete remediations", "Scan.Name", scan.Name)
	}
	if haveOutdatedRems {
		r.recorder.Eventf(
			scan, corev1.EventTypeNormal, "HaveOutdatedRemediations",
			"The scan produced outdated remediations, please check for complianceremediation objects labeled with %s",
			compv1alpha1.OutdatedRemediationLabel)
	}
}

func (r *ReconcileComplianceScan) deleteScriptConfigMaps(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	inNs := client.InNamespace(common.GetComplianceOperatorNamespace())
	withLabel := client.MatchingLabels{
		compv1alpha1.ComplianceScanLabel: instance.Name,
		compv1alpha1.ScriptLabel:         "",
	}
	err := r.client.DeleteAllOf(context.Background(), &corev1.ConfigMap{}, inNs, withLabel)
	if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileComplianceScan) deleteResultConfigMaps(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	inNs := client.InNamespace(common.GetComplianceOperatorNamespace())
	withLabel := client.MatchingLabels{compv1alpha1.ComplianceScanLabel: instance.Name}
	err := r.client.DeleteAllOf(context.Background(), &corev1.ConfigMap{}, inNs, withLabel)
	if err != nil {
		return err
	}
	return nil
}

// returns true if the pod is still running, false otherwise
func isPodRunningInNode(r *ReconcileComplianceScan, scanInstance *compv1alpha1.ComplianceScan, node *corev1.Node, logger logr.Logger) (bool, error) {
	podName := getPodForNodeName(scanInstance.Name, node.Name)
	return isPodRunning(r, podName, common.GetComplianceOperatorNamespace(), logger)
}

// returns true if the pod is still running, false otherwise
func isPlatformScanPodRunning(r *ReconcileComplianceScan, scanInstance *compv1alpha1.ComplianceScan, logger logr.Logger) (bool, error) {
	logger.Info("Retrieving platform scan pod.", "Name", scanInstance.Name+"-"+PlatformScanName)

	podName := getPodForNodeName(scanInstance.Name, PlatformScanName)
	return isPodRunning(r, podName, common.GetComplianceOperatorNamespace(), logger)
}

func isPodRunning(r *ReconcileComplianceScan, podName, namespace string, logger logr.Logger) (bool, error) {
	podlogger := logger.WithValues("Pod.Name", podName)
	foundPod := &corev1.Pod{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: namespace}, foundPod)
	if err != nil {
		podlogger.Error(err, "Cannot retrieve pod")
		return false, err
	} else if foundPod.Status.Phase == corev1.PodSucceeded {
		podlogger.Info("Pod has finished")
		return false, nil
	}

	// Check PodScheduled condition
	for _, condition := range foundPod.Status.Conditions {
		if condition.Type == corev1.PodScheduled {
			if condition.Reason == corev1.PodReasonUnschedulable {
				podlogger.Info("Pod unschedulable")
				return false, newPodUnschedulableError(foundPod.Name, condition.Message)
			}
			break
		}
	}

	// We check for failured in the end, as we want to make sure that conditions
	// are checked first.
	if foundPod.Status.Phase == corev1.PodFailed {
		podlogger.Info("Pod failed. It should be restarted.", "Reason", foundPod.Status.Reason, "Message", foundPod.Status.Message)
		// We mark this as if the pod is still running, as it should be
		// restarted by the kubelet due to the restart policy
		return true, nil
	}

	// the pod is still running or being created etc
	podlogger.Info("Pod still running")
	return true, nil
}

func getPlatformScanCM(r *ReconcileComplianceScan, instance *compv1alpha1.ComplianceScan) (*corev1.ConfigMap, error) {
	targetCM := types.NamespacedName{
		Name:      getConfigMapForNodeName(instance.Name, PlatformScanName),
		Namespace: common.GetComplianceOperatorNamespace(),
	}

	foundCM := &corev1.ConfigMap{}
	err := r.client.Get(context.TODO(), targetCM, foundCM)
	return foundCM, err
}

func getNodeScanCM(r *ReconcileComplianceScan, instance *compv1alpha1.ComplianceScan, nodeName string) (*corev1.ConfigMap, error) {
	targetCM := types.NamespacedName{
		Name:      getConfigMapForNodeName(instance.Name, nodeName),
		Namespace: common.GetComplianceOperatorNamespace(),
	}

	foundCM := &corev1.ConfigMap{}
	err := r.client.Get(context.TODO(), targetCM, foundCM)
	return foundCM, err
}

// gatherResults will iterate the nodes in the scan and get the results
// for the OpenSCAP check. If the results haven't yet been persisted in
// the relevant ConfigMap, the a requeue will be requested since the
// results are not ready.
func gatherResults(r *ReconcileComplianceScan, h scanTypeHandler) (compv1alpha1.ComplianceScanStatusResult, bool, error) {
	instance := h.getScan()

	result, isReady, err := h.gatherResults()

	if err != nil {
		return result, isReady, err
	}

	// If there are any inconsistent results, always just return
	// the state as inconsistent unless there was an error earlier
	var checkList compv1alpha1.ComplianceCheckResultList
	checkListOpts := client.MatchingLabels{
		compv1alpha1.ComplianceCheckInconsistentLabel: "",
		compv1alpha1.ComplianceScanLabel:              instance.Name,
	}
	if err := r.client.List(context.TODO(), &checkList, &checkListOpts); err != nil {
		isReady = false
	}
	if len(checkList.Items) > 0 {
		return compv1alpha1.ResultInconsistent, isReady,
			fmt.Errorf("results were not consistent, search for compliancecheckresults labeled with %s",
				compv1alpha1.ComplianceCheckInconsistentLabel)
	}

	return result, isReady, nil
}

// pod names are limited to 63 chars, inclusive. Try to use a friendly name, if that can't be done,
// just use a hash. Either way, the node would be present in a label of the pod.
func getPodForNodeName(scanName, nodeName string) string {
	return utils.DNSLengthName("openscap-pod-", "%s-%s-pod", scanName, nodeName)
}

func getConfigMapForNodeName(scanName, nodeName string) string {
	return utils.DNSLengthName("openscap-pod-", "%s-%s-pod", scanName, nodeName)
}

func getInitContainerImage(scanSpec *compv1alpha1.ComplianceScanSpec, logger logr.Logger) string {
	image := utils.GetComponentImage(utils.CONTENT)

	if scanSpec.ContentImage != "" {
		image = scanSpec.ContentImage
	}

	logger.Info("Content image", "image", image)
	return image
}
