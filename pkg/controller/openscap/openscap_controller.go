package openscap

import (
	"context"
	"fmt"
	"strings"

	openscapv1alpha1 "github.com/jhrozek/openscap-operator/pkg/apis/openscap/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
)

var log = logf.Log.WithName("controller_openscap")

var (
	trueVal     = true
	hostPathDir = corev1.HostPathDirectory
)

// Add creates a new OpenScap Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileOpenScap{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("openscap-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource OpenScap
	err = c.Watch(&source.Kind{Type: &openscapv1alpha1.OpenScap{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner OpenScap
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &openscapv1alpha1.OpenScap{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileOpenScap implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileOpenScap{}

// ReconcileOpenScap reconciles a OpenScap object
type ReconcileOpenScap struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a OpenScap object and makes changes based on the state read
// and what is in the OpenScap.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileOpenScap) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling OpenScap")

	// Fetch the OpenScap instance
	instance := &openscapv1alpha1.OpenScap{}
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

	// If no phase set, default to pending (the initial phase):
	if instance.Status.Phase == "" {
		instance.Status.Phase = openscapv1alpha1.PhasePending
	}

	switch instance.Status.Phase {
	case openscapv1alpha1.PhasePending:
		return r.phasePendingHandler(instance, reqLogger)
	case openscapv1alpha1.PhaseLaunching:
		return r.phaseLaunchingHandler(instance, reqLogger)
	case openscapv1alpha1.PhaseRunning:
		return r.phaseRunningHandler(instance, reqLogger)
	case openscapv1alpha1.PhaseDone:
		return r.phaseDoneHandler(instance, reqLogger)
	}

	// the default catch-all, just requeue
	return reconcile.Result{}, nil
}

func (r *ReconcileOpenScap) phasePendingHandler(instance *openscapv1alpha1.OpenScap, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Pending", "OpenScap scan", instance.ObjectMeta.Name)

	// Update the scan instance, the next phase is running
	instance.Status.Phase = openscapv1alpha1.PhaseLaunching
	err := r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	// TODO: It might be better to store the list of eligible nodes in the CR so that if someone edits the CR or
	// adds/removes nodes while the scan is running, we just work on the same set?

	return reconcile.Result{}, nil
}

func (r *ReconcileOpenScap) phaseLaunchingHandler(instance *openscapv1alpha1.OpenScap, logger logr.Logger) (reconcile.Result, error) {
	var nodes corev1.NodeList
	var err error

	logger.Info("Phase: Launching", "OpenScap scan", instance.ObjectMeta.Name)

	if nodes, err = getTargetNodes(r); err != nil {
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
		if err != nil {
			log.Error(err, "Failed to launch a pod", "pod", pod)
			return reconcile.Result{}, err
		}
		logger.Info("Launched a pod", "pod", pod)
	}

	// if we got here, there are no new pods to be created, move to the next phase
	instance.Status.Phase = openscapv1alpha1.PhaseRunning
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileOpenScap) phaseRunningHandler(instance *openscapv1alpha1.OpenScap, logger logr.Logger) (reconcile.Result, error) {
	var nodes corev1.NodeList
	var err error

	logger.Info("Phase: Running", "OpenScap scan", instance.ObjectMeta.Name)

	if nodes, err = getTargetNodes(r); err != nil {
		log.Error(err, "Cannot get nodes")
		return reconcile.Result{}, err
	}

	// TODO: test no eligible nodes in the cluster? should just loop through, though..

	// On each eligible node..
	for _, node := range nodes.Items {
		running, err := getPodForNode(r, instance, &node, logger)
		if err != nil {
			return reconcile.Result{}, err
		}

		if running {
			// at least one pod is still running, just go back to the queue
			return reconcile.Result{}, err
		}
	}

	// if we got here, there are no pods running, move to the Done phase
	instance.Status.Phase = openscapv1alpha1.PhaseDone
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileOpenScap) phaseDoneHandler(instance *openscapv1alpha1.OpenScap, logger logr.Logger) (reconcile.Result, error) {
	logger.Info("Phase: Done", "OpenScap scan", instance.ObjectMeta.Name)
	// Todo maybe clean up the pods?
	return reconcile.Result{}, nil
}

func getTargetNodes(r *ReconcileOpenScap) (corev1.NodeList, error) {
	var nodes corev1.NodeList

	// TODO: Use a selector
	listOpts := client.ListOptions{}

	if err := r.client.List(context.TODO(), &listOpts, &nodes); err != nil {
		return nodes, err
	}

	return nodes, nil
}

// returns true if the pod is still running, false otherwise
func getPodForNode(r *ReconcileOpenScap, openScapCr *openscapv1alpha1.OpenScap, node *corev1.Node, logger logr.Logger) (bool, error) {
	logger.Info("Retrieving a pod for node", "node", node.Name)

	podName := fmt.Sprintf("%s-%s-pod", openScapCr.Name, node.Name)
	foundPod := &corev1.Pod{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: openScapCr.Namespace}, foundPod)
	if err != nil && errors.IsNotFound(err) {
		// The pod no longer exists, this is OK
		return false, nil
	} else if err != nil {
		logger.Error(err, "Cannot retrieve pod", "pod", podName)
		return false, err
	} else if foundPod.Status.Phase == corev1.PodFailed || foundPod.Status.Phase == corev1.PodSucceeded {
		logger.Info("Pod on node has finished", "node", node.Name)
		return false, nil
	}

	// the pod is still running or being created etc
	logger.Info("Pod on node still running", "node", node.Name)
	return true, nil

}

func newPodForNode(openScapCr *openscapv1alpha1.OpenScap, node *corev1.Node, logger logr.Logger) *corev1.Pod {
	logger.Info("Creating a pod for node", "node", node.Name)

	// FIXME: this is for now..
	podName := fmt.Sprintf("%s-%s-pod", openScapCr.Name, node.Name)
	podLabels := map[string]string{
		"openscapScan": openScapCr.Name,
		"targetNode":   node.Name,
	}
	openScapContainerEnv := getOscapContainerEnv(&openScapCr.Spec, logger)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: openScapCr.Namespace,
			Labels:    podLabels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "openscap-operator",
			Containers: []corev1.Container{
				{
					Name:  "log-collector",
					Image: "quay.io/jhrozek/scapresults-k8s:latest",
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
					Name:  "openscap-ocp",
					Image: "quay.io/jhrozek/openscap-ocp:latest",
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
			},
		},
	}
}

// TODO: this probably should not be a method, it doesn't modify reconciler, maybe we
// should just pass reconciler as param
func (r *ReconcileOpenScap) launchPod(pod *corev1.Pod, logger logr.Logger) error {
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

func getOscapContainerEnv(scanSpec *openscapv1alpha1.OpenScapSpec, logger logr.Logger) []corev1.EnvVar {
	content := scanSpec.Content
	if !strings.HasPrefix(scanSpec.Content, "/") {
		content = "/var/lib/content/" + scanSpec.Content
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
