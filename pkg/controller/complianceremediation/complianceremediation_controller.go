package complianceremediation

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("remediationctrl")

const (
	remediationNameAnnotationKey = "remediation/"
)

// Add creates a new ComplianceRemediation Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileComplianceRemediation{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("complianceremediation-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource ComplianceRemediation
	err = c.Watch(&source.Kind{Type: &compv1alpha1.ComplianceRemediation{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileComplianceRemediation implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileComplianceRemediation{}

// ReconcileComplianceRemediation reconciles a ComplianceRemediation object
type ReconcileComplianceRemediation struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a ComplianceRemediation object and makes changes based on the state read
// and what is in the ComplianceRemediation.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileComplianceRemediation) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	var err error

	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling ComplianceRemediation")

	// Fetch the ComplianceRemediation instance
	remediationInstance := &compv1alpha1.ComplianceRemediation{}
	err = r.client.Get(context.TODO(), request.NamespacedName, remediationInstance)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Cannot retrieve remediation")
		return reconcile.Result{}, err
	}

	if remediationInstance.Spec.Current.Object != nil {
		reqLogger.Info("Reconciling remediation")
		err = r.reconcileRemediation(remediationInstance, reqLogger)
	} else {
		err = fmt.Errorf("No remediation specified. spec.object is empty")
		return common.ReturnWithRetriableError(reqLogger, common.WrapNonRetriableCtrlError(err))
	}

	// this would have been much nicer with go 1.13 using errors.Is()
	// Only return if the error is retriable. Else, we persist it in the status
	if err != nil && common.IsRetriable(err) {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	// Second, we'll reconcile the status of the Remediation itself
	err = r.reconcileRemediationStatus(remediationInstance, reqLogger, err)
	// this would have been much nicer with go 1.13 using errors.Is()
	if err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	reqLogger.Info("Done reconciling")
	return reconcile.Result{}, nil
}

// Gets a remediation and ensures the object exists in the cluster if the
// remediation if applicable
func (r *ReconcileComplianceRemediation) reconcileRemediation(instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	obj := getApplicableObject(instance, logger)
	if obj == nil {
		return common.NewNonRetriableCtrlError("Invalid Remediation: No object given")
	}
	if utils.IsMachineConfig(obj) {
		if err := r.verifyAndCompleteMC(obj, instance); err != nil {
			return err
		}
	}

	objectLogger := logger.WithValues("Object.Name", obj.GetName(), "Object.Namespace", obj.GetNamespace(), "Object.Kind", obj.GetKind())
	objectLogger.Info("Reconciling remediation object")

	found := obj.DeepCopy()
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, found)

	if kerrors.IsForbidden(err) {
		return common.NewNonRetriableCtrlError(
			"Unable to get fix object from ComplianceRemediation. "+
				"Please update the compliance-operator's permissions: %s", err)
	} else if meta.IsNoMatchError(err) {
		// If the kind is not available in the cluster, we can't retry
		return common.NewNonRetriableCtrlError(
			"Unable to get fix object for ComplianceRemediation. "+
				"Make sure the CRD is installed: %s", err)
	} else if kerrors.IsNotFound(err) {
		if instance.Spec.Apply {
			instance.AddOwnershipLabels(obj)
			return r.createRemediation(obj, objectLogger)
		}

		objectLogger.Info("The object wasn't found, so no action is needed to unapply it")
		return nil
	} else if err != nil {
		return err
	}

	if instance.Spec.Apply {
		return r.patchRemediation(obj, objectLogger)
	}

	return r.deleteRemediation(obj, found, objectLogger)
}

func (r *ReconcileComplianceRemediation) createRemediation(remObj *unstructured.Unstructured, logger logr.Logger) error {
	logger.Info("Remediation will be created")
	compv1alpha1.AddRemediationAnnotation(remObj)

	createErr := r.client.Create(context.TODO(), remObj)

	if kerrors.IsForbidden(createErr) {
		// If the kind is not available in the cluster, we can't retry
		return common.NewNonRetriableCtrlError(
			"Unable to create fix object from ComplianceRemediation. "+
				" Please update the compliance-operator's permissions: %s", createErr)
	}

	return createErr
}

func (r *ReconcileComplianceRemediation) patchRemediation(remObj *unstructured.Unstructured, logger logr.Logger) error {
	logger.Info("Remediation patch object")

	patchErr := r.client.Patch(context.TODO(), remObj, client.Merge)

	if kerrors.IsForbidden(patchErr) {
		// If the kind is not available in the cluster, we can't retry
		return common.NewNonRetriableCtrlError(
			"Unable to patch fix object from ComplianceRemediation. "+
				"Please update the compliance-operator's permissions: %s", patchErr)
	}

	return patchErr

}

func (r *ReconcileComplianceRemediation) deleteRemediation(remObj *unstructured.Unstructured, foundObj *unstructured.Unstructured, logger logr.Logger) error {
	logger.Info("Remediation will be deleted")

	if !compv1alpha1.RemediationWasCreatedByOperator(foundObj) {
		logger.Info("Can't unapply since this object wasn't created by the operator")
		return nil
	}
	deleteErr := r.client.Delete(context.TODO(), remObj)

	if kerrors.IsForbidden(deleteErr) {
		return common.NewNonRetriableCtrlError(
			"Unable to delete fix object from ComplianceRemediation. "+
				"Please update the compliance-operator's permissions: %s", deleteErr)
	} else if kerrors.IsNotFound(deleteErr) {
		return nil
	}

	return deleteErr
}

func (r *ReconcileComplianceRemediation) reconcileRemediationStatus(instance *compv1alpha1.ComplianceRemediation,
	logger logr.Logger, errorApplying error) error {
	instanceCopy := instance.DeepCopy()
	if errorApplying != nil {
		instanceCopy.Status.ApplicationState = compv1alpha1.RemediationError
		instanceCopy.Status.ErrorMessage = errorApplying.Error()
		logger.Info("Remediation had an error")
	} else if instance.Spec.Apply {
		if instanceCopy.Spec.Outdated.Object != nil {
			instanceCopy.Status.ApplicationState = compv1alpha1.RemediationOutdated
			logger.Info("Remediation remains outdated")
		} else {
			instanceCopy.Status.ApplicationState = compv1alpha1.RemediationApplied
			logger.Info("Remediation will now be applied")
		}
	} else {
		instanceCopy.Status.ApplicationState = compv1alpha1.RemediationNotApplied
		logger.Info("Remediation will now be unapplied")
	}

	if err := r.client.Status().Update(context.TODO(), instanceCopy); err != nil {
		logger.Error(err, "Failed to update the remediation status")
		// This should be retried
		return err
	}

	return nil
}

func (r *ReconcileComplianceRemediation) verifyAndCompleteMC(obj *unstructured.Unstructured, rem *compv1alpha1.ComplianceRemediation) error {
	scan := &compv1alpha1.ComplianceScan{}
	scanKey := types.NamespacedName{Name: rem.Labels[compv1alpha1.ComplianceScanLabel], Namespace: rem.Namespace}
	if err := r.client.Get(context.TODO(), scanKey, scan); err != nil {
		return fmt.Errorf("couldn't get scan for MC remediation: %w", err)
	}

	mcfgpools := &mcfgv1.MachineConfigPoolList{}
	if err := r.client.List(context.TODO(), mcfgpools); err != nil {
		return fmt.Errorf("couldn't list the pools for the remediation: %w", err)
	}
	// The scans contain a nodeSelector that ultimately must match a machineConfigPool. The only way we can
	// ensure it does is by checking if it matches any MachineConfigPool's labels.
	// See also: https://github.com/openshift/machine-config-operator/blob/master/docs/custom-pools.md
	if !utils.AnyMcfgPoolLabelMatches(scan.Spec.NodeSelector, mcfgpools) {
		return common.NewNonRetriableCtrlError("not applying remediation that doesn't have a matching MachineconfigPool. Scan: %s", scan.Name)
	}

	obj.SetName(rem.GetMcName())

	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[mcfgv1.MachineConfigRoleLabelKey] = utils.GetFirstNodeRole(scan.Spec.NodeSelector)
	obj.SetLabels(labels)

	return nil
}

func getApplicableObject(instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) *unstructured.Unstructured {
	if instance.Spec.Outdated.Object != nil {
		logger.Info("Using the outdated content")
		return instance.Spec.Outdated.Object.DeepCopy()
	} else if instance.Spec.Current.Object != nil {
		logger.Info("Using the current content")
		return instance.Spec.Current.Object.DeepCopy()
	}
	logger.Info("No object in remediation")
	return nil
}
