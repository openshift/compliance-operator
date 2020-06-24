package compliancescan

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
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
	"github.com/openshift/compliance-operator/pkg/utils"
)

var log = logf.Log.WithName("scanctrl")

var oneReplica int32 = 1

var (
	trueVal     = true
	hostPathDir = corev1.HostPathDirectory
)

const (
	// OpenSCAPScanContainerName defines the name of the contianer that will run OpenSCAP
	OpenSCAPScanContainerName = "openscap-ocp"
	// The default time we should wait before requeuing
	requeueAfterDefault = 10 * time.Second
)

// Add creates a new ComplianceScan Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileComplianceScan{client: mgr.GetClient(), scheme: mgr.GetScheme(), recorder: mgr.GetEventRecorderFor("scanctrl")}
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

	// If no phase set, default to pending (the initial phase):
	if scanToBeUpdated.Status.Phase == "" {
		scanToBeUpdated.Status.Phase = compv1alpha1.PhasePending
	}

	switch scanToBeUpdated.Status.Phase {
	case compv1alpha1.PhasePending:
		return r.phasePendingHandler(scanToBeUpdated, reqLogger)
	case compv1alpha1.PhaseLaunching:
		return r.phaseLaunchingHandler(scanToBeUpdated, reqLogger)
	case compv1alpha1.PhaseRunning:
		return r.phaseRunningHandler(scanToBeUpdated, reqLogger)
	case compv1alpha1.PhaseAggregating:
		return r.phaseAggregatingHandler(scanToBeUpdated, reqLogger)
	case compv1alpha1.PhaseDone:
		return r.phaseDoneHandler(scanToBeUpdated, reqLogger)
	}

	// the default catch-all, just remove the request from the queue
	return reconcile.Result{}, nil
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

	err := createConfigMaps(r, scriptCmForScan(instance), envCmForScan(instance), envCmForPlatformScan(instance), instance)
	if err != nil {
		logger.Error(err, "Cannot create the configmaps")
		return reconcile.Result{}, err
	}

	// Update the scan instance, the next phase is running
	instance.Status.Phase = compv1alpha1.PhaseLaunching
	instance.Status.Result = compv1alpha1.ResultNotAvailable
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		logger.Error(err, "Cannot update the status")
		return reconcile.Result{}, err
	}

	// TODO: It might be better to store the list of eligible nodes in the CR so that if someone edits the CR or
	// adds/removes nodes while the scan is running, we just work on the same set?

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseLaunchingHandler(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	var nodes corev1.NodeList
	var err error

	logger.Info("Phase: Launching")

	if nodes, err = getTargetNodes(r, instance); err != nil {
		log.Error(err, "Cannot get nodes")
		return reconcile.Result{}, err
	}

	if err = r.handleRootCASecret(instance, logger); err != nil {
		log.Error(err, "Cannot create CA secret")
		return reconcile.Result{}, err
	}

	if err = r.handleResultServerSecret(instance, logger); err != nil {
		log.Error(err, "Cannot create result server cert secret")
		return reconcile.Result{}, err
	}

	if err = r.handleResultClientSecret(instance, logger); err != nil {
		log.Error(err, "Cannot create result client cert secret")
		return reconcile.Result{}, err
	}

	if resume, err := r.handleRawResultsForScan(instance, logger); err != nil || !resume {
		if err != nil {
			logger.Error(err, "Cannot create the PersistentVolumeClaims")
		}
		return reconcile.Result{}, err
	}

	if err = r.createResultServer(instance, logger); err != nil {
		log.Error(err, "Cannot create result server")
		return reconcile.Result{}, err
	}

	if err = r.createScanPods(instance, nodes, logger); err != nil {
		if !common.IsRetriable(err) {
			// Surface non-retriable errors to the CR
			log.Info("Updating scan status due to unretriable error")
			scanCopy := instance.DeepCopy()
			scanCopy.Status.ErrorMessage = err.Error()
			scanCopy.Status.Result = compv1alpha1.ResultError
			scanCopy.Status.Phase = compv1alpha1.PhaseDone
			if updateerr := r.client.Status().Update(context.TODO(), scanCopy); updateerr != nil {
				log.Error(updateerr, "Failed to update a scan")
				return reconcile.Result{}, updateerr
			}
		}
		return common.ReturnWithRetriableError(logger, err)
	}

	// if we got here, there are no new pods to be created, move to the next phase
	instance.Status.Phase = compv1alpha1.PhaseRunning
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseRunningHandler(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	var nodes corev1.NodeList
	var err error

	logger.Info("Phase: Running")

	switch instance.Spec.ScanType {
	case compv1alpha1.ScanTypePlatform:
		running, err := isPlatformScanPodRunning(r, instance, logger)
		if errors.IsNotFound(err) {
			// Let's go back to the previous state and make sure all the nodes are covered.
			logger.Info("Phase: Running: The platform scan pod is missing. Going to state LAUNCHING to make sure we launch it",
				"compliancescan")
			instance.Status.Phase = compv1alpha1.PhaseLaunching
			err = r.client.Status().Update(context.TODO(), instance)
			if err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		} else if err != nil {
			return reconcile.Result{}, err
		}

		if running {
			// The platform scan pod is still running, go back to queue.
			return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, err
		}
	default: // ScanTypeNode
		if nodes, err = getTargetNodes(r, instance); err != nil {
			log.Error(err, "Cannot get nodes")
			return reconcile.Result{}, err
		}

		if len(nodes.Items) == 0 {
			log.Info("Warning: No eligible nodes. Check the nodeSelector.")
		}

		// On each eligible node..
		for _, node := range nodes.Items {
			running, err := isPodRunningInNode(r, instance, &node, logger)
			if errors.IsNotFound(err) {
				// Let's go back to the previous state and make sure all the nodes are covered.
				logger.Info("Phase: Running: A pod is missing. Going to state LAUNCHING to make sure we launch it",
					"compliancescan", instance.ObjectMeta.Name, "node", node.Name)
				instance.Status.Phase = compv1alpha1.PhaseLaunching
				err = r.client.Status().Update(context.TODO(), instance)
				if err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{}, nil
			} else if err != nil {
				return reconcile.Result{}, err
			}
			if running {
				// at least one pod is still running, just go back to the queue
				return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, err
			}
		}
	}

	// if we got here, there are no pods running, move to the Aggregating phase
	instance.Status.Phase = compv1alpha1.PhaseAggregating
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseAggregatingHandler(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Aggregating")

	var nodes corev1.NodeList
	var err error

	if nodes, err = getTargetNodes(r, instance); err != nil {
		log.Error(err, "Cannot get nodes")
		return reconcile.Result{}, err
	}

	isReady, err := shouldLaunchAggregator(r, instance, nodes)

	// We only wait if there are no errors.
	if err == nil && !isReady {
		log.Info("ConfigMap missing (not ready). Requeuing.")
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	if err != nil {
		instance.Status.Phase = compv1alpha1.PhaseDone
		instance.Status.Result = compv1alpha1.ResultError
		instance.Status.ErrorMessage = err.Error()
		err = r.updateStatusWithEvent(instance, logger)
		return reconcile.Result{}, err
	}

	logger.Info("Creating an aggregator pod for scan")
	aggregator := newAggregatorPod(instance, logger)
	err = r.launchAggregatorPod(instance, aggregator, logger)
	if err != nil {
		log.Error(err, "Failed to launch aggregator pod", "aggregator", aggregator)
		return reconcile.Result{}, err
	}

	running, err := isAggregatorRunning(r, instance, logger)
	if errors.IsNotFound(err) {
		// Suppress loud error message by requeueing
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault / 2}, nil
	} else if err != nil {
		log.Error(err, "Failed to check if aggregator pod is running", "aggregator", aggregator)
		return reconcile.Result{}, err
	}

	if running {
		log.Info("Remaining in the aggregating phase")
		instance.Status.Phase = compv1alpha1.PhaseAggregating
		err = r.client.Status().Update(context.TODO(), instance)
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	log.Info("Moving on to the Done phase")

	result, isReady, err := gatherResults(r, instance, nodes)

	// We only wait if there are no errors.
	if err == nil && !isReady {
		log.Info("ConfigMap missing (not ready). Requeuing.")
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	instance.Status.Result = result
	if err != nil {
		instance.Status.ErrorMessage = err.Error()
	}

	instance.Status.Phase = compv1alpha1.PhaseDone
	err = r.updateStatusWithEvent(instance, logger)
	return reconcile.Result{}, err
}

func (r *ReconcileComplianceScan) phaseDoneHandler(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	var nodes corev1.NodeList
	var err error
	logger.Info("Phase: Done")

	// We need to remove resources before doing a re-scan
	if !instance.Spec.Debug || instance.NeedsRescan() {
		logger.Info("Cleaning up scan's resources")
		switch instance.Spec.ScanType {
		case compv1alpha1.ScanTypePlatform:
			if err := r.deletePlatformScanPod(instance, logger); err != nil {
				log.Error(err, "Cannot delete platform scan pod")
				return reconcile.Result{}, err
			}
		default: // ScanTypeNode
			if nodes, err = getTargetNodes(r, instance); err != nil {
				log.Error(err, "Cannot get nodes")
				return reconcile.Result{}, err
			}

			if err := r.deleteScanPods(instance, nodes, logger); err != nil {
				log.Error(err, "Cannot delete scan pods")
				return reconcile.Result{}, err
			}
		}

		if err := r.deleteResultServer(instance, logger); err != nil {
			log.Error(err, "Cannot delete result server")
			return reconcile.Result{}, err
		}

		if err := r.deleteAggregator(instance, logger); err != nil {
			log.Error(err, "Cannot delete aggregator")
			return reconcile.Result{}, err
		}

		if err = r.deleteResultServerSecret(instance, logger); err != nil {
			log.Error(err, "Cannot delete result server cert secret")
			return reconcile.Result{}, err
		}

		if err = r.deleteResultClientSecret(instance, logger); err != nil {
			log.Error(err, "Cannot delete result client cert secret")
			return reconcile.Result{}, err
		}

		if err = r.deleteRootCASecret(instance, logger); err != nil {
			log.Error(err, "Cannot delete CA secret")
			return reconcile.Result{}, err
		}

		if err = r.deleteScriptConfigMaps(instance, logger); err != nil {
			log.Error(err, "Cannot delete script ConfigMaps")
			return reconcile.Result{}, err
		}

		if instance.NeedsRescan() {
			if err = r.deleteResultConfigMaps(instance, logger); err != nil {
				log.Error(err, "Cannot delete result ConfigMaps")
				return reconcile.Result{}, err
			}

			// reset phase
			log.Info("Resetting scan")
			instanceCopy := instance.DeepCopy()
			instanceCopy.Status.Phase = compv1alpha1.PhasePending
			instanceCopy.Status.Result = compv1alpha1.ResultNotAvailable
			if instance.Status.CurrentIndex == math.MaxInt64 {
				instanceCopy.Status.CurrentIndex = 0
			} else {
				instanceCopy.Status.CurrentIndex = instance.Status.CurrentIndex + 1
			}
			err = r.client.Status().Update(context.TODO(), instanceCopy)
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) scanDeleteHandler(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	if common.ContainsFinalizer(instance.ObjectMeta.Finalizers, compv1alpha1.ScanFinalizer) {
		logger.Info("The scan is being deleted")
		scanToBeDeleted := instance.DeepCopy()

		// Force remove rescan annotation since we're deleting the scan
		if scanToBeDeleted.NeedsRescan() {
			delete(scanToBeDeleted.Annotations, compv1alpha1.ComplianceScanRescanAnnotation)
		}
		// Force debug to be false so we actually remove dependent objects
		// NOTE(jaosorior): When we're in debug mode, objects don't get deleted
		// to aide in debugging. if we're actually going through a deletion as
		// is the case in this code-path, then we REALLY want to make sure everything
		// gets deleted.
		scanToBeDeleted.Spec.Debug = false

		// remove objects by forcing handling of phase DONE
		if _, err := r.phaseDoneHandler(scanToBeDeleted, logger); err != nil {
			// if fail to delete the external dependency here, return with error
			// so that it can be retried
			return reconcile.Result{}, err
		}

		if err := r.deleteResultConfigMaps(scanToBeDeleted, logger); err != nil {
			log.Error(err, "Cannot delete result ConfigMaps")
			return reconcile.Result{}, err
		}

		if err := r.deleteRawResultsForScan(scanToBeDeleted); err != nil {
			log.Error(err, "Cannot delete raw results")
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
			scan, corev1.EventTypeNormal, "ScanNotApplicable",
			"The scan result is not applicable, please check if you're using the correct platform")
	} else if scan.Status.Result == compv1alpha1.ResultInconsistent {
		r.recorder.Eventf(
			scan, corev1.EventTypeNormal, "ScanNotConsistent",
			"The scan result is not consistent, please check for scan results labeled with %s",
			compv1alpha1.ComplianceCheckInconsistentLabel)
	}
}

func getTargetNodes(r *ReconcileComplianceScan, instance *compv1alpha1.ComplianceScan) (corev1.NodeList, error) {
	var nodes corev1.NodeList

	switch instance.Spec.ScanType {
	case compv1alpha1.ScanTypePlatform:
		return nodes, nil // Nodes are only relevant to the node scan type. Return the empty node list otherwise.
	default:
		listOpts := client.ListOptions{
			LabelSelector: labels.SelectorFromSet(instance.Spec.NodeSelector),
		}

		if err := r.client.List(context.TODO(), &nodes, &listOpts); err != nil {
			return nodes, err
		}
	}

	return nodes, nil
}

func (r *ReconcileComplianceScan) deleteScriptConfigMaps(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	inNs := client.InNamespace(common.GetComplianceOperatorNamespace())
	withLabel := client.MatchingLabels{
		compv1alpha1.ScanLabel:   instance.Name,
		compv1alpha1.ScriptLabel: "",
	}
	err := r.client.DeleteAllOf(context.Background(), &corev1.ConfigMap{}, inNs, withLabel)
	if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileComplianceScan) deleteResultConfigMaps(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	inNs := client.InNamespace(common.GetComplianceOperatorNamespace())
	withLabel := client.MatchingLabels{compv1alpha1.ComplianceScanIndicatorLabel: instance.Name}
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
	foundPod := &corev1.Pod{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: namespace}, foundPod)
	if err != nil {
		logger.Error(err, "Cannot retrieve pod", "Pod.Name", podName)
		return false, err
	} else if foundPod.Status.Phase == corev1.PodFailed || foundPod.Status.Phase == corev1.PodSucceeded {
		logger.Info("Pod has finished")
		return false, nil
	}

	// the pod is still running or being created etc
	logger.Info("Pod still running", "Pod.Name", podName)
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

// shouldLaunchAggregator is a check that tests whether the scanner already failed
// hard in which case there might not be a reason to launch the aggregator pod, e.g.
// in cases the content cannot be loaded at all
func shouldLaunchAggregator(r *ReconcileComplianceScan, instance *compv1alpha1.ComplianceScan, nodes corev1.NodeList) (bool, error) {
	switch instance.Spec.ScanType {
	case compv1alpha1.ScanTypePlatform:
		foundCM, err := getPlatformScanCM(r, instance)

		// Could be a transient error, so we requeue if there's any
		// error here.
		if err != nil {
			return false, nil
		}

		// NOTE: err is only set if there is an error in the scan run
		err = checkScanError(foundCM)
		if err != nil {
			return true, err
		}
	default: // ScanTypeNode
		for _, node := range nodes.Items {
			foundCM, err := getNodeScanCM(r, instance, node.Name)

			// Could be a transient error, so we requeue if there's any
			// error here.
			if err != nil {
				return false, nil
			}

			// NOTE: err is only set if there is an error in the scan run
			err = checkScanError(foundCM)
			if err != nil {
				return true, err
			}
		}
	}

	return true, nil
}

// gatherResults will iterate the nodes in the scan and get the results
// for the OpenSCAP check. If the results haven't yet been persisted in
// the relevant ConfigMap, the a requeue will be requested since the
// results are not ready.
func gatherResults(r *ReconcileComplianceScan, instance *compv1alpha1.ComplianceScan, nodes corev1.NodeList) (compv1alpha1.ComplianceScanStatusResult, bool, error) {
	var lastNonCompliance compv1alpha1.ComplianceScanStatusResult
	var result compv1alpha1.ComplianceScanStatusResult
	compliant := true
	isReady := true

	switch instance.Spec.ScanType {
	case compv1alpha1.ScanTypePlatform:
		foundCM, err := getPlatformScanCM(r, instance)

		// Could be a transient error, so we requeue if there's any
		// error here.
		if err != nil {
			isReady = false
		}

		// NOTE: err is only set if there is an error in the scan run
		result, err = getScanResult(foundCM)

		// we output the last result if it was an error
		if result == compv1alpha1.ResultError {
			return result, true, err
		}
		// Store the last non-compliance, so we can output that if
		// there were no errors.
		if result == compv1alpha1.ResultNonCompliant {
			lastNonCompliance = result
			compliant = false
		}
	default: // ScanTypeNode
		for _, node := range nodes.Items {
			foundCM, err := getNodeScanCM(r, instance, node.Name)

			// Could be a transient error, so we requeue if there's any
			// error here.
			if err != nil {
				isReady = false
			}

			// NOTE: err is only set if there is an error in the scan run
			result, err = getScanResult(foundCM)

			// we output the last result if it was an error
			if result == compv1alpha1.ResultError {
				return result, true, err
			}
			// Store the last non-compliance, so we can output that if
			// there were no errors.
			if result == compv1alpha1.ResultNonCompliant {
				lastNonCompliance = result
				compliant = false
			}
		}
	}

	// If there are any inconsistent results, always just return
	// the state as inconsistent unless there was an error earlier
	var checkList compv1alpha1.ComplianceCheckResultList
	checkListOpts := client.MatchingLabels{compv1alpha1.ComplianceCheckInconsistentLabel: ""}
	if err := r.client.List(context.TODO(), &checkList, &checkListOpts); err != nil {
		isReady = false
	}
	if len(checkList.Items) > 0 {
		return compv1alpha1.ResultInconsistent, isReady,
			fmt.Errorf("results were not consistent, search for compliancecheckresults labeled with %s",
				compv1alpha1.ComplianceCheckInconsistentLabel)
	}

	if !compliant {
		return lastNonCompliance, isReady, nil
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
	image := DefaultContentContainerImage

	if scanSpec.ContentImage != "" {
		image = scanSpec.ContentImage
	}

	logger.Info("Content image", "image", image)
	return image
}
