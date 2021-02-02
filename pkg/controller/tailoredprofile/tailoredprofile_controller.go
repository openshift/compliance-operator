package tailoredprofile

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/openshift/compliance-operator/pkg/xccdf"

	compliancev1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
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

var log = logf.Log.WithName("tailoredprofilectrl")

const (
	tailoringFile string = "tailoring.xml"
)

// Add creates a new TailoredProfile Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileTailoredProfile{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("tailoredprofile-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource TailoredProfile
	err = c.Watch(&source.Kind{Type: &compliancev1alpha1.TailoredProfile{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &compliancev1alpha1.TailoredProfile{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileTailoredProfile implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileTailoredProfile{}

// ReconcileTailoredProfile reconciles a TailoredProfile object
type ReconcileTailoredProfile struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a TailoredProfile object and makes changes based on the state read
// and what is in the TailoredProfile.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileTailoredProfile) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling TailoredProfile")

	// Fetch the TailoredProfile instance
	instance := &compliancev1alpha1.TailoredProfile{}
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

	if canContinue, err := r.validateTailoredProfile(instance); !canContinue {
		return reconcile.Result{}, err
	}

	// Get the Profile being extended
	p := &compliancev1alpha1.Profile{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: instance.Spec.Extends, Namespace: instance.Namespace}, p)
	if err != nil {
		if errors.IsNotFound(err) {
			// the Profile object didn't exist. Surface the error.
			err = r.updateTailoredProfileStatusError(instance, err)
			if err != nil {
				// error udpating status - requeue
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		// could be a transient error that can be recovered from
		return reconcile.Result{}, err
	}

	// Make TailoredProfile be owned by the Profile it extends. This way
	// we can ensure garbage collection happens.
	// This update will trigger a requeue with the new object.
	if !isOwnedBy(instance, p) {
		if err := controllerutil.SetControllerReference(p, instance, r.scheme); err != nil {
			return reconcile.Result{}, err
		}
		err = r.client.Update(context.TODO(), instance)
		return reconcile.Result{}, err
	}

	pb, err := r.getProfileBundleFromProfile(p)
	if err != nil {
		return reconcile.Result{}, err
	}

	rules, retriableErr, err := r.getRulesFromSelections(instance)
	if err != nil {
		if !retriableErr {
			// Surface the error.
			err = r.updateTailoredProfileStatusError(instance, err)
			if err != nil {
				// error udpating status - requeue
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	variables, retriableErr, err := r.getVariablesFromSelections(instance)
	if err != nil {
		if !retriableErr {
			// Surface the error.
			err = r.updateTailoredProfileStatusError(instance, err)
			if err != nil {
				// error udpating status - requeue
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Get tailored profile config map
	tpcm := newTailoredProfileCM(instance)

	tpcm.Data[tailoringFile], err = xccdf.TailoredProfileToXML(instance, p, pb, rules, variables)
	if err != nil {
		return reconcile.Result{}, err
	}

	return r.ensureOutputObject(instance, tpcm, pb, reqLogger)
}

// validates the given TailoredProfile. true means that we can continue since the
// tailored profile is valid, false means that we can't.
func (r *ReconcileTailoredProfile) validateTailoredProfile(tp *compliancev1alpha1.TailoredProfile) (bool, error) {
	// Validate TailoredProfile
	if tp.Spec.Extends == "" {
		err := r.updateTailoredProfileStatusError(tp, fmt.Errorf(".spec.extends can't be empty"))
		if err != nil {
			return false, err
		}
		// don't return an error in the reconciler. The error will surface via the CR's status
		return false, nil
	}

	return true, nil
}

func (r *ReconcileTailoredProfile) getRulesFromSelections(tp *compliancev1alpha1.TailoredProfile) (map[string]*compliancev1alpha1.Rule, bool, error) {
	rules := make(map[string]*compliancev1alpha1.Rule)
	for _, selection := range append(tp.Spec.EnableRules, tp.Spec.DisableRules...) {
		_, ok := rules[selection.Name]
		if ok {
			return nil, false, fmt.Errorf("Rule '%s' appears twice in selections (enableRules or disableRules)", selection.Name)
		}
		rule := &compliancev1alpha1.Rule{}
		ruleKey := types.NamespacedName{Name: selection.Name, Namespace: tp.Namespace}
		err := r.client.Get(context.TODO(), ruleKey, rule)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, false, err
			}
			return nil, true, err
		}
		rules[selection.Name] = rule
	}
	return rules, false, nil
}

func (r *ReconcileTailoredProfile) getVariablesFromSelections(tp *compliancev1alpha1.TailoredProfile) ([]*compliancev1alpha1.Variable, bool, error) {
	variableList := []*compliancev1alpha1.Variable{}
	for _, setValues := range tp.Spec.SetValues {
		variable := &compliancev1alpha1.Variable{}
		varKey := types.NamespacedName{Name: setValues.Name, Namespace: tp.Namespace}
		err := r.client.Get(context.TODO(), varKey, variable)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, false, err
			}
			return nil, true, err
		}

		// try setting the variable, this also validates the value
		err = variable.SetValue(setValues.Value)
		if err != nil {
			return nil, false, err
		}

		variableList = append(variableList, variable)
	}
	return variableList, false, nil
}

func (r *ReconcileTailoredProfile) updateTailoredProfileStatusReady(tp *compliancev1alpha1.TailoredProfile, out metav1.Object) error {
	// Never update the original (update the copy)
	tpCopy := tp.DeepCopy()
	tpCopy.Status.State = compliancev1alpha1.TailoredProfileStateReady
	tpCopy.Status.OutputRef = compliancev1alpha1.OutputRef{
		Name:      out.GetName(),
		Namespace: out.GetNamespace(),
	}
	tpCopy.Status.ID = xccdf.GetXCCDFProfileID(tp)
	return r.client.Status().Update(context.TODO(), tpCopy)
}

func (r *ReconcileTailoredProfile) updateTailoredProfileStatusError(tp *compliancev1alpha1.TailoredProfile, err error) error {
	// Never update the original (update the copy)
	tpCopy := tp.DeepCopy()
	tpCopy.Status.State = compliancev1alpha1.TailoredProfileStateError
	tpCopy.Status.ErrorMessage = err.Error()
	return r.client.Status().Update(context.TODO(), tpCopy)
}

func (r *ReconcileTailoredProfile) getProfileBundleFromProfile(p *compliancev1alpha1.Profile) (*compliancev1alpha1.ProfileBundle, error) {
	pbRef, err := getProfileBundleReferenceFromProfile(p)
	if err != nil {
		return nil, err
	}

	pb := compliancev1alpha1.ProfileBundle{}
	// we use the profile's namespace as either way the object's have to be in the same namespace
	// in order for OwnerReferences to work
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: pbRef.Name, Namespace: p.Namespace}, &pb)
	return &pb, err
}

func (r *ReconcileTailoredProfile) ensureOutputObject(tp *compliancev1alpha1.TailoredProfile, tpcm *corev1.ConfigMap, pb *compliancev1alpha1.ProfileBundle, logger logr.Logger) (reconcile.Result, error) {
	// Set TailoredProfile instance as the owner and controller
	if err := controllerutil.SetControllerReference(tp, tpcm, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this ConfigMap already exists
	found := &corev1.ConfigMap{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: tpcm.Name, Namespace: tpcm.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// update status
		err = r.updateTailoredProfileStatusReady(tp, tpcm)
		if err != nil {
			fmt.Printf("Couldn't update TailoredProfile status: %v\n", err)
			return reconcile.Result{}, err
		}

		// create CM
		logger.Info("Creating a new ConfigMap", "ConfigMap.Namespace", tpcm.Namespace, "ConfigMap.Name", tpcm.Name)
		err = r.client.Create(context.TODO(), tpcm)
		if err != nil {
			return reconcile.Result{}, err
		}

		// ConfigMap created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// ConfigMap already exists - don't requeue
	logger.Info("Skip reconcile: ConfigMap already exists", "ConfigMap.Namespace", found.Namespace, "ConfigMap.Name", found.Name)
	return reconcile.Result{}, nil
}

func getProfileBundleReferenceFromProfile(p *compliancev1alpha1.Profile) (*metav1.OwnerReference, error) {
	for _, ref := range p.GetOwnerReferences() {
		if ref.Kind == "ProfileBundle" && ref.APIVersion == compliancev1alpha1.SchemeGroupVersion.String() {
			return ref.DeepCopy(), nil
		}
	}
	return nil, fmt.Errorf("Profile '%s' had no owning ProfileBundle", p.Name)
}

// newTailoredProfileCM creates a tailored profile XML inside a configmap
func newTailoredProfileCM(tp *compliancev1alpha1.TailoredProfile) *corev1.ConfigMap {
	labels := map[string]string{
		"tailored-profile": tp.Name,
	}
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      tp.Name + "-tp",
			Namespace: tp.Namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			tailoringFile: "",
		},
	}
}

func isOwnedBy(obj, owner metav1.Object) bool {
	refs := obj.GetOwnerReferences()
	for _, ref := range refs {
		if ref.UID == owner.GetUID() {
			return true
		}
	}
	return false
}
