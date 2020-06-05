package profilebundle

import (
	"context"

	// #nosec G505

	"fmt"
	"path"

	"github.com/go-logr/logr"
	compliancev1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_profilebundle")

// Add creates a new ProfileBundle Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileProfileBundle{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("profilebundle-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource ProfileBundle
	err = c.Watch(&source.Kind{Type: &compliancev1alpha1.ProfileBundle{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner ProfileBundle
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &compliancev1alpha1.ProfileBundle{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileProfileBundle implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileProfileBundle{}

// ReconcileProfileBundle reconciles a ProfileBundle object
type ReconcileProfileBundle struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a ProfileBundle object and makes changes based on the state read
// and what is in the ProfileBundle.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileProfileBundle) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling ProfileBundle")

	// Fetch the ProfileBundle instance
	instance := &compliancev1alpha1.ProfileBundle{}
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
		if !common.ContainsFinalizer(instance.ObjectMeta.Finalizers, compliancev1alpha1.ProfileBundleFinalizer) {
			pb := instance.DeepCopy()
			pb.ObjectMeta.Finalizers = append(pb.ObjectMeta.Finalizers, compliancev1alpha1.ProfileBundleFinalizer)
			if err := r.client.Update(context.TODO(), pb); err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
	} else {
		// The object is being deleted
		return reconcile.Result{}, r.profileBundleDeleteHandler(instance, reqLogger)
	}

	// Define a new Pod object
	pod := newPodForBundle(instance)

	// Set ProfileBundle instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, pod, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this Pod already exists
	found := &corev1.Pod{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		pbCopy := instance.DeepCopy()
		pbCopy.Status.DataStreamStatus = compliancev1alpha1.DataStreamPending
		err = r.client.Status().Update(context.TODO(), pbCopy)
		if err != nil {
			reqLogger.Error(err, "Couldn't update ProfileBundle status")
			return reconcile.Result{}, err
		}
		reqLogger.Info("Creating a new Pod", "Pod.Namespace", pod.Namespace, "Pod.Name", pod.Name)
		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Pod created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	if podStartupError(found) {
		// report to status
		pbCopy := instance.DeepCopy()
		pbCopy.Status.DataStreamStatus = compliancev1alpha1.DataStreamInvalid
		pbCopy.Status.ErrorMessage = "The init container failed to start. Check Status.ContentImage."
		err = r.client.Status().Update(context.TODO(), pbCopy)
		if err != nil {
			reqLogger.Error(err, "Couldn't update ProfileBundle status")
			return reconcile.Result{}, err
		}
		// this was a fatal error, don't requeue
		return reconcile.Result{}, nil
	}

	// Pod already exists and its init container at least ran - don't requeue
	reqLogger.Info("Skip reconcile: Pod already exists", "Pod.Namespace", found.Namespace, "Pod.Name", found.Name)
	return reconcile.Result{}, nil
}

func (r *ReconcileProfileBundle) profileBundleDeleteHandler(pb *compliancev1alpha1.ProfileBundle, logger logr.Logger) error {
	logger.Info("The ProfileBundle is being deleted")
	pod := newPodForBundle(pb)
	logger.Info("Deleting profileparser pod", "Pod.Name", pod.Name)
	err := r.client.Delete(context.TODO(), pod)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	pbCopy := pb.DeepCopy()
	// remove our finalizer from the list and update it.
	pbCopy.ObjectMeta.Finalizers = common.RemoveFinalizer(pbCopy.ObjectMeta.Finalizers, compliancev1alpha1.ProfileBundleFinalizer)
	if err := r.client.Update(context.TODO(), pbCopy); err != nil {
		return err
	}
	return nil
}

// newPodForBundle returns a busybox pod with the same name/namespace as the cr
func newPodForBundle(pb *compliancev1alpha1.ProfileBundle) *corev1.Pod {
	labels := map[string]string{
		"profile-bundle": pb.Name,
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pb.Name + "-pp",
			Namespace: common.GetComplianceOperatorNamespace(),
			Labels:    labels,
		},

		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{
					Name:  "content-container",
					Image: pb.Spec.ContentImage,
					Command: []string{
						"sh",
						"-c",
						fmt.Sprintf("cp %s /content | /bin/true", path.Join("/", pb.Spec.ContentFile)),
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
					Name:  "profileparser",
					Image: utils.GetComponentImage(utils.OPERATOR),
					Command: []string{
						"compliance-operator", "profileparser",
						"--name", pb.Name,
						"--namespace", pb.Namespace,
						"--ds-path", path.Join("/content", pb.Spec.ContentFile),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "content-dir",
							MountPath: "/content",
							ReadOnly:  true,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
			//ServiceAccountName: "profileparser",
			ServiceAccountName: "compliance-operator",
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

// podStartupError returns false if for some reason the pod couldn't even
// run. If there's more conditions in the function in the future, let's
// split it
func podStartupError(pod *corev1.Pod) bool {
	// Check if the init container couldn't even run because the content image
	// was wrong
	for _, initStatus := range pod.Status.InitContainerStatuses {
		if initStatus.Ready == true {
			// in case there was a transient error before we reconciled,
			// just shortcut the loop and return false
			break
		}

		if initStatus.State.Waiting == nil {
			continue
		}

		switch initStatus.State.Waiting.Reason {
		case "ImagePullBackOff", "ErrImagePull":
			return true
		}
	}

	return false
}
