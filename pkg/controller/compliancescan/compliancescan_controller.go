package compliancescan

import (
	"context"
	// we can suppress the gosec warning about sha1 here because we don't use sha1 for crypto
	// purposes, but only as a string shortener
	// #nosec G505
	"crypto/sha1"
	"fmt"
	"io"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	case complianceoperatorv1alpha1.PhaseDone:
		return r.phaseDoneHandler(scanToBeUpdated, reqLogger)
	}

	// the default catch-all, just remove the request from the queue
	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phasePendingHandler(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Pending", "ComplianceScan", instance.ObjectMeta.Name)

	// Update the scan instance, the next phase is running
	instance.Status.Phase = complianceoperatorv1alpha1.PhaseLaunching
	err := r.client.Status().Update(context.TODO(), instance)
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

	// TODO: test no eligible nodes in the cluster? should just loop through, though..

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
		}
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

	// if we got here, there are no pods running, move to the Done phase
	instance.Status.Phase = complianceoperatorv1alpha1.PhaseDone
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceScan) phaseDoneHandler(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Done", "ComplianceScan scan", instance.ObjectMeta.Name)

	var nodes corev1.NodeList
	var err error

	if nodes, err = getTargetNodes(r, instance); err != nil {
		log.Error(err, "Cannot get nodes")
		return reconcile.Result{}, err
	}

	result, err := gatherResults(r, instance, nodes)

	instance.Status.Result = result
	if err != nil {
		instance.Status.ErrorMessage = err.Error()
	}
	err = r.client.Status().Update(context.TODO(), instance)
	return reconcile.Result{}, err
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

// returns true if the pod is still running, false otherwise
func isPodRunningInNode(r *ReconcileComplianceScan, openScapCr *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node, logger logr.Logger) (bool, error) {
	logger.Info("Retrieving a pod for node", "node", node.Name)

	podName := podForNodeName(openScapCr.Name, node.Name)
	foundPod := &corev1.Pod{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: openScapCr.Namespace}, foundPod)
	if err != nil {
		logger.Error(err, "Cannot retrieve pod", "pod", podName)
		return false, err
	} else if foundPod.Status.Phase == corev1.PodFailed || foundPod.Status.Phase == corev1.PodSucceeded {
		logger.Info("Pod on node has finished", "node", node.Name)
		return false, nil
	} else if aContainerHasFailed(foundPod.Status.ContainerStatuses) {
		logger.Info("Container on the pod on node has failed", "node", node.Name, "pod", podName)
		return false, nil
	}

	// the pod is still running or being created etc
	logger.Info("Pod on node still running", "node", node.Name)
	return true, nil
}

func aContainerHasFailed(statuses []corev1.ContainerStatus) bool {
	for _, status := range statuses {
		if status.State.Terminated != nil {
			if status.State.Terminated.ExitCode != 0 {
				return true
			}
		}
	}
	return false
}

func getScanResult(r *ReconcileComplianceScan, instance *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node) (complianceoperatorv1alpha1.ComplianceScanStatusResult, error) {
	podName := podForNodeName(instance.Name, node.Name)
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

func newPodForNode(openScapCr *complianceoperatorv1alpha1.ComplianceScan, node *corev1.Node, logger logr.Logger) *corev1.Pod {
	logger.Info("Creating a pod for node", "node", node.Name)

	// FIXME: this is for now..
	podName := podForNodeName(openScapCr.Name, node.Name)
	podLabels := map[string]string{
		"complianceScan": openScapCr.Name,
		"targetNode":     node.Name,
	}
	openScapContainerEnv := getOscapContainerEnv(&openScapCr.Spec)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: openScapCr.Namespace,
			Labels:    podLabels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "compliance-operator",
			InitContainers: []corev1.Container{
				{
					Name:  "content-container",
					Image: getInitContainerImage(&openScapCr.Spec, logger),
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
						"--owner=" + openScapCr.Name,
						"--namespace=" + openScapCr.Namespace,
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueVal,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "report-dir",
							MountPath: "/reports",
						},
					},
				},
				{
					Name:  OpenSCAPScanContainerName,
					Image: GetComponentImage(OPENSCAP),
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
					},
					Env: openScapContainerEnv,
				},
			},
			NodeName:      node.Name,
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
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
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

// pod names are limited to 63 chars, inclusive. Try to use a friendly name, if that can't be done,
// just use a hash. Either way, the node would be present in a label of the pod.
func podForNodeName(scanName, nodeName string) string {
	const maxPodLen = 63

	// first, try to use the full scan and node name
	podName := fmt.Sprintf("%s-%s-pod", scanName, nodeName)
	if len(podName) < maxPodLen {
		return podName
	}

	// if that's too long, just hash the name. It's not very user friendly, but whatever
	// we could in theory also use the nodeName up to the first dot, but that would open
	// up conflicts, so let's not..
	// we can suppress the gosec warning about sha1 here because we don't use sha1 for crypto
	// purposes, but only as a string shortener
	// #nosec G401
	hasher := sha1.New()
	io.WriteString(hasher, fmt.Sprintf("%s-%s-pod", scanName, nodeName))
	return "openscap-pod-" + fmt.Sprintf("%x", hasher.Sum(nil))
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

func getOscapContainerEnv(scanSpec *complianceoperatorv1alpha1.ComplianceScanSpec) []corev1.EnvVar {
	content := scanSpec.Content
	if !strings.HasPrefix(scanSpec.Content, "/") {
		content = "/content/" + scanSpec.Content
	}

	env := []corev1.EnvVar{
		{
			Name:  "HOSTROOT",
			Value: "/host",
		},
		{
			Name:  "PROFILE",
			Value: scanSpec.Profile,
		},
		{
			Name:  "CONTENT",
			Value: content,
		},
		{
			Name:  "REPORT_DIR",
			Value: "/reports",
		},
	}

	if scanSpec.Rule != "" {
		env = append(env, corev1.EnvVar{
			Name:  "RULE",
			Value: scanSpec.Rule,
		})
	}

	return env
}

func getInitContainerImage(scanSpec *complianceoperatorv1alpha1.ComplianceScanSpec, logger logr.Logger) string {
	image := DefaultContentContainerImage

	if scanSpec.ContentImage != "" {
		image = scanSpec.ContentImage
	}

	logger.Info("Content image", "image", image)
	return image
}
