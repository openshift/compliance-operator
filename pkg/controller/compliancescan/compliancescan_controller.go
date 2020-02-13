package compliancescan

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
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
)

var log = logf.Log.WithName("controller_compliancescan")

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

	err = r.createPVCsForScan(instance, nodes.Items)
	if err != nil {
		logger.Error(err, "Cannot create the PersistentVolumeClaims")
		return reconcile.Result{}, err
	}

	// On each eligible node..
	for _, node := range nodes.Items {
		// ..schedule a pod..
		pod := newPodForNode(instance, &node, logger)
		if err = controllerutil.SetControllerReference(instance, pod, r.scheme); err != nil {
			log.Error(err, "Failed to set pod ownership", "pod", pod)
			return reconcile.Result{}, err
		}

		// ..and launch it..
		err := r.launchPod(pod, logger)
		if errors.IsAlreadyExists(err) {
			logger.Info("Pod already exists. This is fine.", "pod", pod)
		} else if err != nil {
			log.Error(err, "Failed to launch a pod", "pod", pod)
			return reconcile.Result{}, err
		} else {
			logger.Info("Launched a pod", "pod", pod)
			// ..since the pod name can be random, store it in a label
			setPodForNodeName(instance, node.Name, pod.Name)
		}
	}

	// make sure the instance is updated with the node-pod labels
	err = r.client.Update(context.TODO(), instance)
	if err != nil {
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

	// TODO: test no eligible nodes in the cluster? should just loop through, though..

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

	result, err := gatherResults(r, instance, nodes)

	instance.Status.Result = result
	instance.Status.Phase = complianceoperatorv1alpha1.PhaseDone
	if err != nil {
		instance.Status.ErrorMessage = err.Error()
	}
	err = r.client.Status().Update(context.TODO(), instance)
	return reconcile.Result{}, err
}

func (r *ReconcileComplianceScan) phaseDoneHandler(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Done", "ComplianceScan scan", instance.ObjectMeta.Name)
	return reconcile.Result{}, nil
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

func (r *ReconcileComplianceScan) createPVCsForScan(instance *complianceoperatorv1alpha1.ComplianceScan, nodes []corev1.Node) error {
	for _, node := range nodes {
		pvc := getPVCForNodeScan(instance, &node)
		if err := controllerutil.SetControllerReference(instance, pvc, r.scheme); err != nil {
			log.Error(err, "Failed to set pvc ownership", "pvc", pvc.Name)
			return err
		}
		if err := r.client.Create(context.TODO(), pvc); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}

// returns true if the pod is still running, false otherwise
func isPodRunningInNode(r *ReconcileComplianceScan, scanInstance *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node, logger logr.Logger) (bool, error) {
	logger.Info("Retrieving a pod for node", "node", node.Name)

	podName := getPodForNodeName(scanInstance, node.Name)
	foundPod := &corev1.Pod{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: scanInstance.Namespace}, foundPod)
	if err != nil {
		logger.Error(err, "Cannot retrieve pod", "pod", podName)
		return false, err
	} else if foundPod.Status.Phase == corev1.PodFailed || foundPod.Status.Phase == corev1.PodSucceeded {
		logger.Info("Pod on node has finished", "node", node.Name)
		return false, nil
	} else if aContainerHasFailed(foundPod.Status.ContainerStatuses, logger, foundPod.Name) {
		logger.Info("Container on the pod on node has failed", "node", node.Name, "pod", podName)
		return false, nil
	}

	// the pod is still running or being created etc
	logger.Info("Pod on node still running", "node", node.Name)
	return true, nil
}

func aContainerHasFailed(statuses []corev1.ContainerStatus, logger logr.Logger, podname string) bool {
	for _, status := range statuses {
		if status.State.Terminated != nil {
			if status.State.Terminated.ExitCode != 0 {
				logger.Info("container failed in pod",
					"pod", podname, "container", status.Name,
					"exit-code", status.State.Terminated.ExitCode)
				return true
			}
		}
	}
	return false
}

func getScanResult(r *ReconcileComplianceScan, instance *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node) (complianceoperatorv1alpha1.ComplianceScanStatusResult, error) {
	podName := getPodForNodeName(instance, node.Name)
	p := &corev1.Pod{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: instance.Namespace}, p)
	if err != nil {
		return complianceoperatorv1alpha1.ResultError, err
	}
	for _, status := range p.Status.ContainerStatuses {
		if status.Name == OpenSCAPScanContainerName {
			if status.State.Terminated != nil {
				switch status.State.Terminated.ExitCode {
				case 0:
					return complianceoperatorv1alpha1.ResultCompliant, nil
				case 2:
					return complianceoperatorv1alpha1.ResultNonCompliant, nil
				default:
					return complianceoperatorv1alpha1.ResultError, fmt.Errorf(status.State.Terminated.Message)
				}
			} else {
				return complianceoperatorv1alpha1.ResultError, fmt.Errorf("The pod '%s' was missing 'terminated' status", p.Name)
			}
		}
	}
	return complianceoperatorv1alpha1.ResultError, fmt.Errorf("Couldn't find '%s' container in '%s' pod status", OpenSCAPScanContainerName, p.Name)
}

func gatherResults(r *ReconcileComplianceScan, instance *complianceoperatorv1alpha1.ComplianceScan, nodes corev1.NodeList) (complianceoperatorv1alpha1.ComplianceScanStatusResult, error) {
	var err error
	var lastNonCompliance complianceoperatorv1alpha1.ComplianceScanStatusResult
	var result complianceoperatorv1alpha1.ComplianceScanStatusResult
	compliant := true
	for _, node := range nodes.Items {
		result, err = getScanResult(r, instance, &node)
		// we output the last result if it was an error
		if result == complianceoperatorv1alpha1.ResultError {
			return result, err
		}
		// Store the last non-compliance, so we can output that if
		// there were no errors.
		if result == complianceoperatorv1alpha1.ResultNonCompliant {
			lastNonCompliance = result
			compliant = false
		}
	}

	if !compliant {
		return lastNonCompliance, nil
	}
	return result, nil
}

func getPVCForNodeScan(instance *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getPVCForNodeScanName(instance, node),
			Namespace: instance.Namespace,
			Labels: map[string]string{
				"complianceScan": instance.Name,
				"targetNode":     node.Name,
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

func newPodForNode(scanInstance *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node, logger logr.Logger) *corev1.Pod {
	logger.Info("Creating a pod for node", "node", node.Name)

	mode := int32(0744)

	podName := createPodForNodeName(scanInstance.Name, node.Name)
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
						"cp /*.xml /content",
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
						"--file=/reports/report.xml",
						"--config-map-name=" + podName,
						"--owner=" + scanInstance.Name,
						"--namespace=" + scanInstance.Namespace,
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueVal,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "report-dir",
							MountPath: "/reports",
							ReadOnly:  true,
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
			RestartPolicy: corev1.RestartPolicyNever,
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
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: getPVCForNodeScanName(scanInstance, node),
						},
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
			},
		},
	}
}

// pod names are limited to 63 chars, inclusive. Try to use a friendly name, if that can't be done,
// just use a hash. Either way, the node would be present in a label of the pod.
func createPodForNodeName(scanName, nodeName string) string {
	return dnsLengthName("openscap-pod-", "%s-%s-pod", scanName, nodeName)
}

func getPodForNodeName(scanInstance *complianceoperatorv1alpha1.ComplianceScan, nodeName string) string {
	return scanInstance.Labels[nodePodLabel(nodeName)]
}

func setPodForNodeName(scanInstance *complianceoperatorv1alpha1.ComplianceScan, nodeName, podName string) {
	if scanInstance.Labels == nil {
		scanInstance.Labels = make(map[string]string)
	}

	scanInstance.Labels[nodePodLabel(nodeName)] = podName
}

func nodePodLabel(nodeName string) string {
	return OpenSCAPNodePodLabel + nodeName
}

func getPVCForNodeScanName(instance *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node) string {
	return instance.Name + "-" + node.Name
}

// TODO: this probably should not be a method, it doesn't modify reconciler, maybe we
// should just pass reconciler as param
func (r *ReconcileComplianceScan) launchPod(pod *corev1.Pod, logger logr.Logger) error {
	found := &corev1.Pod{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, found)
	// Try to see if the pod already exists and if not
	// (which we expect) then create a one-shot pod as per spec:
	if err != nil && errors.IsNotFound(err) {
		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			logger.Error(err, "Cannot create pod", "pod", pod)
			return err
		}
		logger.Info("Pod launched", "name", pod.Name)
		return nil
	} else if err != nil {
		logger.Error(err, "Cannot retrieve pod", "pod", pod)
		return err
	}

	// The pod already exists, re-enter the reconcile loop
	return nil
}

func getInitContainerImage(scanSpec *complianceoperatorv1alpha1.ComplianceScanSpec, logger logr.Logger) string {
	image := DefaultContentContainerImage

	if scanSpec.ContentImage != "" {
		image = scanSpec.ContentImage
	}

	logger.Info("Content image", "image", image)
	return image
}
