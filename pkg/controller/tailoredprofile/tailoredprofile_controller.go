package tailoredprofile

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/xccdf"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
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

	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
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
	err = c.Watch(&source.Kind{Type: &cmpv1alpha1.TailoredProfile{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &cmpv1alpha1.TailoredProfile{},
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
	instance := &cmpv1alpha1.TailoredProfile{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if kerrors.IsNotFound(err) {
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

	p, pb, pbgetErr := r.getProfileInfoFromExtends(instance)
	if pbgetErr != nil && !common.IsRetriable(pbgetErr) {
		// the Profile or ProfileBundle objects didn't exist. Surface the error.
		err = r.updateTailoredProfileStatusError(instance, pbgetErr)
		return reconcile.Result{}, err
	} else if pbgetErr != nil {
		return reconcile.Result{}, pbgetErr
	}

	// Make TailoredProfile be owned by the Profile it extends. This way
	// we can ensure garbage collection happens.
	// This update will trigger a requeue with the new object.
	if needsControllerRef(instance) {
		return r.setOwnership(instance, p)
	}

	rules, ruleErr := r.getRulesFromSelections(instance, pb)
	if ruleErr != nil && !common.IsRetriable(ruleErr) {
		// Surface the error.
		suerr := r.updateTailoredProfileStatusError(instance, ruleErr)
		return reconcile.Result{}, suerr
	} else if ruleErr != nil {
		return reconcile.Result{}, ruleErr
	}

	variables, varErr := r.getVariablesFromSelections(instance, pb)
	if varErr != nil && !common.IsRetriable(varErr) {
		// Surface the error.
		suerr := r.updateTailoredProfileStatusError(instance, varErr)
		return reconcile.Result{}, suerr
	} else if varErr != nil {
		return reconcile.Result{}, varErr
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
func (r *ReconcileTailoredProfile) validateTailoredProfile(tp *cmpv1alpha1.TailoredProfile) (bool, error) {
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

// getProfileBundleFromExtends gets the Profile and ProfileBundleBundle where the rules come from
// out of the profile that's being extended
func (r *ReconcileTailoredProfile) getProfileInfoFromExtends(tp *cmpv1alpha1.TailoredProfile) (*cmpv1alpha1.Profile, *cmpv1alpha1.ProfileBundle, error) {
	p := &cmpv1alpha1.Profile{}
	// Get the Profile being extended
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: tp.Spec.Extends, Namespace: tp.Namespace}, p)
	if kerrors.IsNotFound(err) {
		return nil, nil, common.NewNonRetriableCtrlError("fetching profile to be extended: %w", err)
	}
	if err != nil {
		return nil, nil, err
	}

	pb, err := r.getProfileBundleFrom("Profile", p)
	if err != nil {
		return nil, nil, err
	}

	return p, pb, nil
}

func (r *ReconcileTailoredProfile) getRulesFromSelections(tp *cmpv1alpha1.TailoredProfile, pb *cmpv1alpha1.ProfileBundle) (map[string]*cmpv1alpha1.Rule, error) {
	rules := make(map[string]*cmpv1alpha1.Rule)
	for _, selection := range append(tp.Spec.EnableRules, tp.Spec.DisableRules...) {
		_, ok := rules[selection.Name]
		if ok {
			return nil, common.NewNonRetriableCtrlError("Rule '%s' appears twice in selections (enableRules or disableRules)", selection.Name)
		}
		rule := &cmpv1alpha1.Rule{}
		ruleKey := types.NamespacedName{Name: selection.Name, Namespace: tp.Namespace}
		err := r.client.Get(context.TODO(), ruleKey, rule)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return nil, common.NewNonRetriableCtrlError("Fetching rule: %w", err)
			}
			return nil, err
		}

		// All variables should be part of the same ProfileBundle
		if !isOwnedBy(rule, pb) {
			return nil, common.NewNonRetriableCtrlError("rule %s not owned by expected ProfileBundle %s",
				rule.GetName(), pb.GetName())
		}

		rules[selection.Name] = rule
	}
	return rules, nil
}

func (r *ReconcileTailoredProfile) getVariablesFromSelections(tp *cmpv1alpha1.TailoredProfile, pb *cmpv1alpha1.ProfileBundle) ([]*cmpv1alpha1.Variable, error) {
	variableList := []*cmpv1alpha1.Variable{}
	for _, setValues := range tp.Spec.SetValues {
		variable := &cmpv1alpha1.Variable{}
		varKey := types.NamespacedName{Name: setValues.Name, Namespace: tp.Namespace}
		err := r.client.Get(context.TODO(), varKey, variable)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return nil, common.NewNonRetriableCtrlError("fetching variable: %w", err)
			}
			return nil, err
		}

		// All variables should be part of the same ProfileBundle
		if !isOwnedBy(variable, pb) {
			return nil, common.NewNonRetriableCtrlError("variable %s not owned by expected ProfileBundle %s",
				variable.GetName(), pb.GetName())
		}

		// try setting the variable, this also validates the value
		err = variable.SetValue(setValues.Value)
		if err != nil {
			return nil, common.NewNonRetriableCtrlError("setting variable: %s", err)
		}

		variableList = append(variableList, variable)
	}
	return variableList, nil
}

func (r *ReconcileTailoredProfile) updateTailoredProfileStatusReady(tp *cmpv1alpha1.TailoredProfile, out metav1.Object) error {
	// Never update the original (update the copy)
	tpCopy := tp.DeepCopy()
	tpCopy.Status.State = cmpv1alpha1.TailoredProfileStateReady
	tpCopy.Status.OutputRef = cmpv1alpha1.OutputRef{
		Name:      out.GetName(),
		Namespace: out.GetNamespace(),
	}
	tpCopy.Status.ID = xccdf.GetXCCDFProfileID(tp)
	return r.client.Status().Update(context.TODO(), tpCopy)
}

func (r *ReconcileTailoredProfile) updateTailoredProfileStatusError(tp *cmpv1alpha1.TailoredProfile, err error) error {
	// Never update the original (update the copy)
	tpCopy := tp.DeepCopy()
	tpCopy.Status.State = cmpv1alpha1.TailoredProfileStateError
	tpCopy.Status.ErrorMessage = err.Error()
	return r.client.Status().Update(context.TODO(), tpCopy)
}

func (r *ReconcileTailoredProfile) getProfileBundleFrom(objtype string, o metav1.Object) (*cmpv1alpha1.ProfileBundle, error) {
	pbRef, err := getProfileBundleReference(objtype, o)
	if err != nil {
		return nil, err
	}

	pb := cmpv1alpha1.ProfileBundle{}
	// we use the profile's namespace as either way the object's have to be in the same namespace
	// in order for OwnerReferences to work
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: pbRef.Name, Namespace: o.GetNamespace()}, &pb)
	return &pb, err
}

func (r *ReconcileTailoredProfile) ensureOutputObject(tp *cmpv1alpha1.TailoredProfile, tpcm *corev1.ConfigMap, pb *cmpv1alpha1.ProfileBundle, logger logr.Logger) (reconcile.Result, error) {
	// Set TailoredProfile instance as the owner and controller
	if err := controllerutil.SetControllerReference(tp, tpcm, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this ConfigMap already exists
	found := &corev1.ConfigMap{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: tpcm.Name, Namespace: tpcm.Namespace}, found)
	if err != nil && kerrors.IsNotFound(err) {
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

func (r *ReconcileTailoredProfile) setOwnership(tp *cmpv1alpha1.TailoredProfile, obj metav1.Object) (reconcile.Result, error) {
	if err := controllerutil.SetControllerReference(obj, tp, r.scheme); err != nil {
		return reconcile.Result{}, err
	}
	err := r.client.Update(context.TODO(), tp)
	return reconcile.Result{}, err
}

func getProfileBundleReference(objtype string, o metav1.Object) (*metav1.OwnerReference, error) {
	for _, ref := range o.GetOwnerReferences() {
		if ref.Kind == "ProfileBundle" && ref.APIVersion == cmpv1alpha1.SchemeGroupVersion.String() {
			return ref.DeepCopy(), nil
		}
	}
	return nil, fmt.Errorf("%s '%s' had no owning ProfileBundle", objtype, o.GetName())
}

// newTailoredProfileCM creates a tailored profile XML inside a configmap
func newTailoredProfileCM(tp *cmpv1alpha1.TailoredProfile) *corev1.ConfigMap {
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

func needsControllerRef(obj metav1.Object) bool {
	refs := obj.GetOwnerReferences()
	for _, ref := range refs {
		if ref.Controller != nil {
			if *ref.Controller {
				return false
			}
		}
	}
	return true
}

func isOwnedBy(obj, owner metav1.Object) bool {
	refs := obj.GetOwnerReferences()
	for _, ref := range refs {
		if ref.UID == owner.GetUID() && ref.Name == owner.GetName() {
			return true
		}
	}
	return false
}
