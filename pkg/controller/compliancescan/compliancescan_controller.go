package compliancescan

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
)

var log = logf.Log.WithName("controller_compliancescan")

var oneReplica int32 = 1

var (
	trueVal     = true
	hostPathDir = corev1.HostPathDirectory
)

const (
	// OpenSCAPScanContainerName defines the name of the contianer that will run OpenSCAP
	OpenSCAPScanContainerName = "openscap-ocp"
	OpenSCAPScriptCmLabel     = "cm-script"
	OpenSCAPScriptEnvLabel    = "cm-env"
	OpenSCAPNodePodLabel      = "node-scan/"
	NodeHostnameLabel         = "kubernetes.io/hostname"
	AggregatorPodAnnotation   = "scan-aggregator"
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
	return &ReconcileComplianceScan{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("compliancescan-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource ComplianceScan
	err = c.Watch(&source.Kind{Type: &complianceoperatorv1alpha1.ComplianceScan{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner ComplianceScan
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &complianceoperatorv1alpha1.ComplianceScan{},
	})
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
	client client.Client
	scheme *runtime.Scheme
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
	instance := &complianceoperatorv1alpha1.ComplianceScan{}
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

	// At this point, we make a copy of the instance, so we can modify it in the functions below.
	scanToBeUpdated := instance.DeepCopy()

	// If no phase set, default to pending (the initial phase):
	if scanToBeUpdated.Status.Phase == "" {
		scanToBeUpdated.Status.Phase = complianceoperatorv1alpha1.PhasePending
	}

	switch scanToBeUpdated.Status.Phase {
	case complianceoperatorv1alpha1.PhasePending:
		return r.phasePendingHandler(scanToBeUpdated, reqLogger)
	case complianceoperatorv1alpha1.PhaseLaunching:
		return r.phaseLaunchingHandler(scanToBeUpdated, reqLogger)
	case complianceoperatorv1alpha1.PhaseRunning:
		return r.phaseRunningHandler(scanToBeUpdated, reqLogger)
	case complianceoperatorv1alpha1.PhaseAggregating:
		return r.phaseAggregatingHandler(scanToBeUpdated, reqLogger)
	case complianceoperatorv1alpha1.PhaseDone:
		return r.phaseDoneHandler(scanToBeUpdated, reqLogger)
	}

	// the default catch-all, just remove the request from the queue
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phasePendingHandler(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Pending", "ComplianceScan", instance.ObjectMeta.Name)

	if instance.Labels == nil {
		instance.Labels = make(map[string]string)
	}

	if instance.Labels[OpenSCAPScriptCmLabel] == "" {
		instance.Labels[OpenSCAPScriptCmLabel] = scriptCmForScan(instance)
	}

	if instance.Labels[OpenSCAPScriptEnvLabel] == "" {
		instance.Labels[OpenSCAPScriptEnvLabel] = envCmForScan(instance)
	}

	err := createConfigMaps(r, instance.Labels[OpenSCAPScriptCmLabel], instance.Labels[OpenSCAPScriptEnvLabel], instance)
	if err != nil {
		logger.Error(err, "Cannot create the configmaps")
		return reconcile.Result{}, err
	}

	// Update the labels that hold the name of the configMaps
	err = r.client.Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Update the scan instance, the next phase is running
	instance.Status.Phase = complianceoperatorv1alpha1.PhaseLaunching
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	// TODO: It might be better to store the list of eligible nodes in the CR so that if someone edits the CR or
	// adds/removes nodes while the scan is running, we just work on the same set?

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseLaunchingHandler(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	var nodes corev1.NodeList
	var err error

	logger.Info("Phase: Launching", "ComplianceScan", instance.ObjectMeta.Name)

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

	if err = r.createResultServer(instance, logger); err != nil {
		log.Error(err, "Cannot create result server")
		return reconcile.Result{}, err
	}

	if err = r.createScanPods(instance, nodes, logger); err != nil {
		return reconcile.Result{}, err
	}

	// if we got here, there are no new pods to be created, move to the next phase
	instance.Status.Phase = complianceoperatorv1alpha1.PhaseRunning
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseRunningHandler(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	var nodes corev1.NodeList
	var err error

	logger.Info("Phase: Running", "ComplianceScan scan", instance.ObjectMeta.Name)

	if nodes, err = getTargetNodes(r, instance); err != nil {
		log.Error(err, "Cannot get nodes")
		return reconcile.Result{}, err
	}

	// On each eligible node..
	for _, node := range nodes.Items {
		running, err := isPodRunningInNode(r, instance, &node, logger)
		if errors.IsNotFound(err) {
			// Let's go back to the previous state and make sure all the nodes are covered.
			logger.Info("Phase: Running: A pod is missing. Going to state LAUNCHING to make sure we launch it",
				"compliancescan", instance.ObjectMeta.Name, "node", node.Name)
			instance.Status.Phase = complianceoperatorv1alpha1.PhaseLaunching
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
			return reconcile.Result{}, err
		}
	}

	// if we got here, there are no pods running, move to the Aggregating phase
	instance.Status.Phase = complianceoperatorv1alpha1.PhaseAggregating
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseAggregatingHandler(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Aggregating", "ComplianceScan scan", instance.ObjectMeta.Name)

	var nodes corev1.NodeList
	var err error

	if nodes, err = getTargetNodes(r, instance); err != nil {
		log.Error(err, "Cannot get nodes")
		return reconcile.Result{}, err
	}

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

	logger.Info("Creating an aggregator pod for scan", "scan", instance.Name)
	aggregator := newAggregatorPod(instance, logger)
	if err = controllerutil.SetControllerReference(instance, aggregator, r.scheme); err != nil {
		log.Error(err, "Failed to set aggregator pod ownership", "aggregator", aggregator)
		return reconcile.Result{}, err
	}

	err = r.launchAggregatorPod(instance, aggregator, logger)
	if err != nil {
		log.Error(err, "Failed to launch aggregator pod", "aggregator", aggregator)
		return reconcile.Result{}, err
	}

	running, err := isAggregatorRunning(r, instance, logger)
	if err != nil {
		log.Error(err, "Failed to check if aggregator pod is running", "aggregator", aggregator)
		return reconcile.Result{}, err
	}

	if running {
		log.Info("Remaining in the aggregating phase")
		instance.Status.Phase = complianceoperatorv1alpha1.PhaseAggregating
		err = r.client.Status().Update(context.TODO(), instance)
		return reconcile.Result{Requeue: true, RequeueAfter: requeueAfterDefault}, nil
	}

	log.Info("Moving on to the Done phase")
	instance.Status.Phase = complianceoperatorv1alpha1.PhaseDone
	err = r.client.Status().Update(context.TODO(), instance)
	return reconcile.Result{}, err
}

func (r *ReconcileComplianceScan) phaseDoneHandler(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	var nodes corev1.NodeList
	var err error
	logger.Info("Phase: Done", "ComplianceScan scan", instance.ObjectMeta.Name)
	if !instance.Spec.Debug {
		if nodes, err = getTargetNodes(r, instance); err != nil {
			log.Error(err, "Cannot get nodes")
			return reconcile.Result{}, err
		}

		if err := r.deleteScanPods(instance, nodes, logger); err != nil {
			log.Error(err, "Cannot delete scan pods")
			return reconcile.Result{}, err
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
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) handleRootCASecret(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.client, RootCAPrefix+instance.Name, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating CA", "instance", instance.Name)
	secret, err := makeCASecret(instance, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}

	if err = controllerutil.SetControllerReference(instance, secret, r.scheme); err != nil {
		return err
	}

	// Create the CA secret.
	err = r.client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func (r *ReconcileComplianceScan) handleResultServerSecret(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.client, ServerCertPrefix+instance.Name, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating server cert", "instance", instance.Name)
	secret, err := makeServerCertSecret(r.client, instance, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}

	if err = controllerutil.SetControllerReference(instance, secret, r.scheme); err != nil {
		return err
	}

	// Create the server cert secret.
	err = r.client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *ReconcileComplianceScan) handleResultClientSecret(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.client, ClientCertPrefix+instance.Name, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating client cert", "instance", instance.Name)
	secret, err := makeClientCertSecret(r.client, instance, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}

	if err = controllerutil.SetControllerReference(instance, secret, r.scheme); err != nil {
		return err
	}

	// Create the client cert secret.
	err = r.client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func (r *ReconcileComplianceScan) deleteRootCASecret(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting CA", "instance", instance.Name)
	ns := common.GetComplianceOperatorNamespace()
	secret := certSecret(getCASecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteResultServerSecret(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting server cert", "instance", instance.Name)
	ns := common.GetComplianceOperatorNamespace()
	secret := certSecret(getServerCertSecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteResultClientSecret(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting client cert", "instance", instance.Name)
	ns := common.GetComplianceOperatorNamespace()
	secret := certSecret(getClientCertSecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteSecret(secret *corev1.Secret) error {
	// Delete the client cert secret.
	err := r.client.Delete(context.TODO(), secret)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

func getTargetNodes(r *ReconcileComplianceScan, instance *complianceoperatorv1alpha1.ComplianceScan) (corev1.NodeList, error) {
	var nodes corev1.NodeList

	listOpts := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(instance.Spec.NodeSelector),
	}

	if err := r.client.List(context.TODO(), &nodes, &listOpts); err != nil {
		return nodes, err
	}

	return nodes, nil
}

func (r *ReconcileComplianceScan) createScanPods(instance *complianceoperatorv1alpha1.ComplianceScan, nodes corev1.NodeList, logger logr.Logger) error {
	// On each eligible node..
	for _, node := range nodes.Items {
		// ..schedule a pod..
		logger.Info("Creating a pod for node", "node", node.Name)
		pod := newScanPodForNode(instance, &node, logger)
		if err := controllerutil.SetControllerReference(instance, pod, r.scheme); err != nil {
			log.Error(err, "Failed to set pod ownership", "pod", pod)
			return err
		}

		// ..and launch it..
		err := r.client.Create(context.TODO(), pod)
		if errors.IsAlreadyExists(err) {
			logger.Info("Pod already exists. This is fine.", "pod", pod)
		} else if err != nil {
			log.Error(err, "Failed to launch a pod", "pod", pod)
			return err
		} else {
			logger.Info("Launched a pod", "pod", pod)
			// ..since the pod name can be random, store it in a label
			setPodForNodeName(instance, node.Name, pod.Name)
		}
	}

	// make sure the instance is updated with the node-pod labels
	if err := r.client.Update(context.TODO(), instance); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileComplianceScan) deleteScanPods(instance *complianceoperatorv1alpha1.ComplianceScan, nodes corev1.NodeList, logger logr.Logger) error {
	// On each eligible node..
	for _, node := range nodes.Items {
		// ..schedule a pod..
		logger.Info("Creating a pod for node", "node", node.Name)
		pod := newScanPodForNode(instance, &node, logger)

		// ..and launch it..
		err := r.client.Delete(context.TODO(), pod)
		if errors.IsNotFound(err) {
			logger.Info("Pod is already gone. This is fine.", "pod", pod)
		} else if err != nil {
			log.Error(err, "Failed to delete a pod", "pod", pod)
			return err
		} else {
			logger.Info("deleted pod", "pod", pod)
		}
	}

	return nil
}

// The result-server is a pod that listens for results from other pods and
// stores them in a PVC.
// It's comprised of the PVC for the scan, the pod and a service that fronts it
func (r *ReconcileComplianceScan) createResultServer(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	err := r.createPVCForScan(instance)
	if err != nil {
		logger.Error(err, "Cannot create the PersistentVolumeClaims")
		return err
	}

	resultServerLabels := map[string]string{
		"complianceScan": instance.Name,
		"app":            "resultserver",
	}

	logger.Info("Creating scan result server pod")
	deployment := resultServer(instance, resultServerLabels, logger)
	if err = controllerutil.SetControllerReference(instance, deployment, r.scheme); err != nil {
		log.Error(err, "Failed to set deployment ownership", "deployment", deployment)
		return err
	}

	err = r.client.Create(context.TODO(), deployment)
	if err != nil && !errors.IsAlreadyExists(err) {
		logger.Error(err, "Cannot create deployment", "deployment", deployment)
		return err
	}
	logger.Info("ResultServer Deployment launched", "name", deployment.Name)

	service := resultServerService(instance, resultServerLabels)
	if err = controllerutil.SetControllerReference(instance, service, r.scheme); err != nil {
		log.Error(err, "Failed to set service ownership", "service", service)
		return err
	}

	err = r.client.Create(context.TODO(), service)
	if err != nil && !errors.IsAlreadyExists(err) {
		logger.Error(err, "Cannot create service", "service", service)
		return err
	}
	logger.Info("ResultServer Service launched", "name", service.Name)
	return nil
}

func (r *ReconcileComplianceScan) deleteResultServer(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	resultServerLabels := map[string]string{
		"complianceScan": instance.Name,
		"app":            "resultserver",
	}

	logger.Info("Deleting scan result server pod")

	deployment := resultServer(instance, resultServerLabels, logger)

	err := r.client.Delete(context.TODO(), deployment)
	if err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Cannot delete deployment", "deployment", deployment)
		return err
	}
	logger.Info("ResultServer Deployment deleted", "name", deployment.Name)
	logger.Info("Deleting scan result server service")

	service := resultServerService(instance, resultServerLabels)
	err = r.client.Delete(context.TODO(), service)
	if err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Cannot delete service", "service", service)
		return err
	}
	logger.Info("ResultServer Service deleted", "name", service.Name)
	return nil
}

func (r *ReconcileComplianceScan) createPVCForScan(instance *complianceoperatorv1alpha1.ComplianceScan) error {
	pvc := getPVCForScan(instance)
	if err := controllerutil.SetControllerReference(instance, pvc, r.scheme); err != nil {
		log.Error(err, "Failed to set pvc ownership", "pvc", pvc.Name)
		return err
	}
	if err := r.client.Create(context.TODO(), pvc); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// returns true if the pod is still running, false otherwise
func isPodRunningInNode(r *ReconcileComplianceScan, scanInstance *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node, logger logr.Logger) (bool, error) {
	logger.Info("Retrieving a pod for node", "node", node.Name)

	podName := getPodForNodeName(scanInstance, node.Name)
	return isPodRunning(r, podName, scanInstance.Namespace, logger)
}

func isPodRunning(r *ReconcileComplianceScan, podName, namespace string, logger logr.Logger) (bool, error) {
	foundPod := &corev1.Pod{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: namespace}, foundPod)
	if err != nil {
		logger.Error(err, "Cannot retrieve pod", "pod", podName)
		return false, err
	} else if foundPod.Status.Phase == corev1.PodFailed || foundPod.Status.Phase == corev1.PodSucceeded {
		logger.Info("Pod has finished")
		return false, nil
	}

	// the pod is still running or being created etc
	logger.Info("Pod still running", "pod", podName)
	return true, nil
}

func getScanResult(cm *corev1.ConfigMap) (complianceoperatorv1alpha1.ComplianceScanStatusResult, error) {
	exitcode, ok := cm.Data["exit-code"]
	if ok {
		switch exitcode {
		case "0":
			return complianceoperatorv1alpha1.ResultCompliant, nil
		case "2":
			return complianceoperatorv1alpha1.ResultNonCompliant, nil
		default:
			errorMsg, ok := cm.Data["error-msg"]
			if ok {
				return complianceoperatorv1alpha1.ResultError, fmt.Errorf(errorMsg)
			}
			return complianceoperatorv1alpha1.ResultError, fmt.Errorf("The ConfigMap '%s' was missing 'error-msg'", cm.Name)
		}
	}
	return complianceoperatorv1alpha1.ResultError, fmt.Errorf("The ConfigMap '%s' was missing 'exit-code'", cm.Name)
}

// gatherResults will iterate the nodes in the scan and get the results
// for the OpenSCAP check. If the results haven't yet been persisted in
// the relevant ConfigMap, the a requeue will be requested since the
// results are not ready.
func gatherResults(r *ReconcileComplianceScan, instance *complianceoperatorv1alpha1.ComplianceScan, nodes corev1.NodeList) (complianceoperatorv1alpha1.ComplianceScanStatusResult, bool, error) {
	var lastNonCompliance complianceoperatorv1alpha1.ComplianceScanStatusResult
	var result complianceoperatorv1alpha1.ComplianceScanStatusResult
	compliant := true
	isReady := true
	for _, node := range nodes.Items {
		targetCM := types.NamespacedName{
			Name:      getConfigMapForNodeName(instance.Name, node.Name),
			Namespace: instance.Namespace,
		}

		foundCM := &corev1.ConfigMap{}
		err := r.client.Get(context.TODO(), targetCM, foundCM)

		// Could be a transcient error, so we requeue if there's any
		// error here.
		if err != nil {
			isReady = false
		}

		// NOTE: err is only set if there is an error in the scan run
		result, err = getScanResult(foundCM)

		// we output the last result if it was an error
		if result == complianceoperatorv1alpha1.ResultError {
			return result, true, err
		}
		// Store the last non-compliance, so we can output that if
		// there were no errors.
		if result == complianceoperatorv1alpha1.ResultNonCompliant {
			lastNonCompliance = result
			compliant = false
		}

	}

	if !compliant {
		return lastNonCompliance, isReady, nil
	}
	return result, isReady, nil
}

func getPVCForScan(instance *complianceoperatorv1alpha1.ComplianceScan) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getPVCForScanName(instance),
			Namespace: instance.Namespace,
			Labels: map[string]string{
				"complianceScan": instance.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			// NOTE(jaosorior): Currently we don't set a StorageClass
			// so the default will be taken into use.
			// TODO(jaosorior): Make StorageClass configurable
			StorageClassName: nil,
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					// TODO(jaosorior): Make this configurable
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
}

func newScanPodForNode(scanInstance *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node, logger logr.Logger) *corev1.Pod {

	mode := int32(0744)

	podName := createPodForNodeName(scanInstance.Name, node.Name)
	cmName := getConfigMapForNodeName(scanInstance.Name, node.Name)
	podLabels := map[string]string{
		"complianceScan": scanInstance.Name,
		"targetNode":     node.Name,
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: scanInstance.Namespace,
			Labels:    podLabels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "compliance-operator",
			InitContainers: []corev1.Container{
				{
					Name:  "content-container",
					Image: getInitContainerImage(&scanInstance.Spec, logger),
					Command: []string{
						"sh",
						"-c",
						fmt.Sprintf("cp %s /content | /bin/true", path.Join("/", scanInstance.Spec.Content)),
					},
					ImagePullPolicy: corev1.PullAlways,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "content-dir",
							MountPath: "/content",
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "log-collector",
					Image: GetComponentImage(LOG_COLLECTOR),
					Args: []string{
						"--arf-file=/reports/report-arf.xml",
						"--results-file=/reports/report.xml",
						"--exit-code-file=/reports/exit_code",
						"--oscap-output-file=/reports/cmd_output",
						"--config-map-name=" + cmName,
						"--owner=" + scanInstance.Name,
						"--namespace=" + scanInstance.Namespace,
						"--resultserveruri=" + getResultServerURI(scanInstance),
						"--tls-client-cert=/etc/pki/tls/tls.crt",
						"--tls-client-key=/etc/pki/tls/tls.key",
						"--tls-ca=/etc/pki/tls/ca.crt",
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueVal,
					},
					ImagePullPolicy: corev1.PullAlways,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "report-dir",
							MountPath: "/reports",
							ReadOnly:  true,
						},
						{
							Name:      "tls",
							MountPath: "/etc/pki/tls",
						},
					},
				},
				{
					Name:    OpenSCAPScanContainerName,
					Image:   GetComponentImage(OPENSCAP),
					Command: []string{OpenScapScriptPath},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueVal,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "host",
							MountPath: "/host",
						},
						{
							Name:      "report-dir",
							MountPath: "/reports",
						},
						{
							Name:      "content-dir",
							MountPath: "/content",
						},
						{
							Name:      scanInstance.Labels[OpenSCAPScriptCmLabel],
							MountPath: "/scripts",
						},
					},
					EnvFrom: []corev1.EnvFromSource{
						{
							ConfigMapRef: &corev1.ConfigMapEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: scanInstance.Labels[OpenSCAPScriptEnvLabel],
								},
							},
						},
					},
				},
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      "node-role.kubernetes.io/master",
					Operator: "Exists",
					Effect:   "NoSchedule",
				},
			},
			NodeSelector: map[string]string{
				NodeHostnameLabel: node.Labels[NodeHostnameLabel],
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
			Volumes: []corev1.Volume{
				{
					Name: "host",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/",
							Type: &hostPathDir,
						},
					},
				},
				{
					Name: "report-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "content-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: scanInstance.Labels[OpenSCAPScriptCmLabel],
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: scanInstance.Labels[OpenSCAPScriptCmLabel],
							},
							DefaultMode: &mode,
						},
					},
				},
				{
					Name: "tls",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: ClientCertPrefix + scanInstance.Name,
						},
					},
				},
			},
		},
	}
}

// Serve up arf reports for a compliance scan with a web service protected by openshift auth (oauth-proxy sidecar).
// Needs corresponding Service (with service-serving cert).
// Need to aggregate reports into one service ? on subdirs?
func resultServer(scanInstance *complianceoperatorv1alpha1.ComplianceScan, labels map[string]string, logger logr.Logger) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getResultServerName(scanInstance),
			Namespace: scanInstance.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &oneReplica,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					// TODO(jaosorior): Should we schedule this in the master nodes only?
					ServiceAccountName: "compliance-operator",
					Containers: []corev1.Container{
						{
							Name:            "result-server",
							Image:           GetComponentImage(RESULT_SERVER),
							ImagePullPolicy: corev1.PullAlways,
							Args: []string{
								"--path=/reports/",
								"--address=0.0.0.0",
								fmt.Sprintf("--port=%d", ResultServerPort),
								"--tls-server-cert=/etc/pki/tls/tls.crt",
								"--tls-server-key=/etc/pki/tls/tls.key",
								"--tls-ca=/etc/pki/tls/ca.crt",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "arfreports",
									MountPath: "/reports",
								},
								{
									Name:      "tls",
									MountPath: "/etc/pki/tls",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "arfreports",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: getPVCForScanName(scanInstance),
								},
							},
						},
						{
							Name: "tls",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: ServerCertPrefix + scanInstance.Name,
								},
							},
						},
					},
				},
			},
		},
	}
}

func resultServerService(scanInstance *complianceoperatorv1alpha1.ComplianceScan, labels map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getResultServerName(scanInstance),
			Namespace: scanInstance.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Protocol: corev1.Protocol("TCP"),
					Port:     ResultServerPort,
				},
			},
		},
	}
}

// pod names are limited to 63 chars, inclusive. Try to use a friendly name, if that can't be done,
// just use a hash. Either way, the node would be present in a label of the pod.
func createPodForNodeName(scanName, nodeName string) string {
	return dnsLengthName("openscap-pod-", "%s-%s-pod", scanName, nodeName)
}

func getConfigMapForNodeName(scanName, nodeName string) string {
	return dnsLengthName("openscap-pod-", "%s-%s-pod", scanName, nodeName)
}

func getPodForNodeName(scanInstance *complianceoperatorv1alpha1.ComplianceScan, nodeName string) string {
	return scanInstance.Labels[nodePodLabel(nodeName)]
}

func setPodForNodeName(scanInstance *complianceoperatorv1alpha1.ComplianceScan, nodeName, podName string) {
	if scanInstance.Labels == nil {
		scanInstance.Labels = make(map[string]string)
	}

	// TODO(jaosorior): Figure out if we still need this after deleting the pods
	// This might be more appropraite as an annotation.
	scanInstance.Labels[nodePodLabel(nodeName)] = podName
}

func nodePodLabel(nodeName string) string {
	return OpenSCAPNodePodLabel + nodeName
}

func getPVCForScanName(instance *complianceoperatorv1alpha1.ComplianceScan) string {
	return instance.Name
}

func getResultServerName(instance *complianceoperatorv1alpha1.ComplianceScan) string {
	return instance.Name + "-rs"
}

func getResultServerURI(instance *complianceoperatorv1alpha1.ComplianceScan) string {
	return "https://" + getResultServerName(instance) + fmt.Sprintf(":%d/", ResultServerPort)
}

func getInitContainerImage(scanSpec *complianceoperatorv1alpha1.ComplianceScanSpec, logger logr.Logger) string {
	image := DefaultContentContainerImage

	if scanSpec.ContentImage != "" {
		image = scanSpec.ContentImage
	}

	logger.Info("Content image", "image", image)
	return image
}

func createAggregatorPodName(scanName string) string {
	return dnsLengthName("aggregator-pod-", "aggregator-pod-%s", scanName)
}

func newAggregatorPod(scanInstance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) *corev1.Pod {
	podName := createAggregatorPodName(scanInstance.Name)

	podLabels := map[string]string{
		"complianceScan": scanInstance.Name,
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: scanInstance.Namespace,
			Labels:    podLabels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "compliance-operator",
			InitContainers: []corev1.Container{
				{
					Name:  "content-container",
					Image: getInitContainerImage(&scanInstance.Spec, logger),
					Command: []string{
						"sh",
						"-c",
						fmt.Sprintf("cp %s /content | /bin/true", path.Join("/", scanInstance.Spec.Content)),
					},
					ImagePullPolicy: corev1.PullAlways,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "content-dir",
							MountPath: "/content",
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "log-collector",
					Image: GetComponentImage(AGGREGATOR),
					Args: []string{
						"--content=" + absContentPath(scanInstance.Spec.Content),
						"--scan=" + scanInstance.Name,
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueVal,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "content-dir",
							MountPath: "/content",
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
			Volumes: []corev1.Volume{
				{
					Name: "content-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}
}

func (r *ReconcileComplianceScan) launchAggregatorPod(scanInstance *complianceoperatorv1alpha1.ComplianceScan, pod *corev1.Pod, logger logr.Logger) error {
	// Make use of optimistic concurrency and just try creating the pod
	err := r.client.Create(context.TODO(), pod)
	if err != nil && !errors.IsAlreadyExists(err) {
		logger.Error(err, "Cannot launch pod", "pod", pod)
		return err
	}

	if errors.IsAlreadyExists(err) {
		// If the pod was already created, just return
		return nil
	}

	if scanInstance.Annotations == nil {
		scanInstance.Annotations = make(map[string]string)
	}

	scanInstance.Annotations[AggregatorPodAnnotation] = pod.Name
	return r.client.Update(context.TODO(), scanInstance)
}

func (r *ReconcileComplianceScan) deleteAggregator(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	aggregator := newAggregatorPod(instance, logger)
	err := r.client.Delete(context.TODO(), aggregator)
	if err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Cannot delete aggregator pod", "pod", aggregator)
		return err
	}

	return nil
}

func isAggregatorRunning(r *ReconcileComplianceScan, scanInstance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (bool, error) {
	logger.Info("Checking aggregator pod for scan", "scan", scanInstance.Name)

	podName := scanInstance.Annotations[AggregatorPodAnnotation]
	return isPodRunning(r, podName, scanInstance.Namespace, logger)
}
