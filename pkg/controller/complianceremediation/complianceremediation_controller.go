package complianceremediation

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
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
	return &ReconcileComplianceRemediation{client: mgr.GetClient(),
		scheme: mgr.GetScheme()}
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
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Cannot retrieve remediation")
		return reconcile.Result{}, err
	}

	if remediationInstance.Spec.Object != nil {
		reqLogger.Info("Reconciling remediation")
		err = r.reconcileRemediation(remediationInstance, reqLogger)
	} else if remediationInstance.Spec.MachineConfigContents != nil {
		reqLogger.Info("updating deprecated MachineConfig remediation")
		err = r.updateDeprecatedMcRemediation(remediationInstance, reqLogger)
	} else {
		err = fmt.Errorf("No remediation specified. Both spec.object and spec.machineconfig are empty")
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

func (r *ReconcileComplianceRemediation) updateDeprecatedMcRemediation(instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	remCopy := instance.DeepCopy()
	remCopy.Spec.Object = remCopy.Spec.MachineConfigContents.DeepCopy()
	remCopy.Spec.MachineConfigContents = nil
	return r.client.Update(context.TODO(), remCopy)
}

func (r *ReconcileComplianceRemediation) reconcileRemediation(instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	if utils.IsMachineConfig(instance.Spec.Object) {
		return r.reconcileMcRemediation(instance, logger)
	}
	return r.reconcileGenericRemediation(instance, logger)
}

// Gets a generic remediation and ensures the object exists in the cluster if the
// remediation if applicable
func (r *ReconcileComplianceRemediation) reconcileGenericRemediation(instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	obj := instance.Spec.Object
	found := obj.DeepCopy()
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, found)
	if instance.Spec.Apply {
		if errors.IsNotFound(err) {
			return r.client.Create(context.TODO(), obj)
		} else if meta.IsNoMatchError(err) {
			// If the kind is not available in the cluster, we can't retry
			return common.NewNonRetriableCtrlError("Unable to create ComplianceRemediation. Make the CRD is installed: %s", err)
		}
		// TODO(jaosorior): If the object is found, should we update it?
		return err
	}
	// unapply remediation
	if err == nil {
		return r.client.Delete(context.TODO(), obj)
	} else if errors.IsNotFound(err) {
		return nil
	} else if meta.IsNoMatchError(err) {
		// If the kind is not available in the cluster, we can't retry
		return common.NewNonRetriableCtrlError("Unable to use ComplianceRemediation. Make the CRD is installed: %s", err)
	}
	return err
}

// reconcileMcRemediation makes sure that the list of selected ComplianceRemediations is reflected in an
// aggregated MachineConfig object. To do that, any remediations that are already selected are listed
// and if the Remediation being reconciled is applied, it is added to the list.
// On the other hand, a Remediation can also be de-selected, this would result in either the resulting
// MC having one less remediation or in the case no remediations are to be applied, the aggregate
// MC is just deleted because it would otherwise be empty
func (r *ReconcileComplianceRemediation) reconcileMcRemediation(instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	// mcList is a combination of remediations already applied and the remediation selected
	// already converted to a list of MachineConfig API resources
	mcList, err := getApplicableMcList(r, instance, logger)
	if err != nil {
		logger.Error(err, "getApplicableMcList failed")
		return err
	}

	// Merge that list of MCs into a single big one MC
	name := instance.GetMcName()
	if name == "" {
		return common.WrapNonRetriableCtrlError(
			fmt.Errorf("could not construct MC name, check if it has the correct labels"))
	}

	logger.Info("Will create or update MC", "MachineConfig.Name", name)
	mergedMc := mergeMachineConfigs(mcList, name, instance.Labels[mcfgv1.MachineConfigRoleLabelKey])

	// if the mergedMc was nil, then we should remove the resulting MC, probably the last selected
	// remediation was deselected
	if mergedMc == nil {
		logger.Info("The merged MC was nil, will delete the old MC", "MachineConfig.Name", name)
		return deleteMachineConfig(r, name, logger)
	}

	// Actually touch the MC, this hands over control to the MCO
	// TODO: Only log this with a very high log level
	// logger.Info("Merged MC", "mc", mergedMc)
	if err := createOrUpdateMachineConfig(r, mergedMc, instance, logger); err != nil {
		logger.Error(err, "Failed to create or modify the MC")
		// The err itself is already retriable (or not)
		return err
	}

	return nil
}

func (r *ReconcileComplianceRemediation) reconcileRemediationStatus(instance *compv1alpha1.ComplianceRemediation,
	logger logr.Logger, errorApplying error) error {
	instanceCopy := instance.DeepCopy()
	if errorApplying != nil {
		instanceCopy.Status.ApplicationState = compv1alpha1.RemediationError
		instanceCopy.Status.ErrorMessage = errorApplying.Error()
		logger.Info("Remediation had an error")
	} else if instance.Spec.Apply {
		instanceCopy.Status.ApplicationState = compv1alpha1.RemediationApplied
		logger.Info("Remediation will now be applied")
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

func getApplicableMcList(r *ReconcileComplianceRemediation, instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) ([]*mcfgv1.MachineConfig, error) {
	// Retrieve all the remediations that are already applied and merge with the one selected (if selected)
	appliedRemediations, err := getAppliedMcRemediations(r, instance)
	if err != nil {
		// The caller already flagged the error for retry
		logger.Error(err, "Cannot get applied remediation list")
		return nil, err
	}
	logger.Info("Found applied remediations", "num", len(appliedRemediations))
	// TODO: Print the names of the applied remediations with a very high log level

	// If the one being reconciled is supposed to be applied as well, add it to the list
	if instance.Spec.Apply == true {
		scan := &compv1alpha1.ComplianceScan{}
		scanKey := types.NamespacedName{Name: instance.Labels[compv1alpha1.ScanLabel], Namespace: instance.Namespace}
		if err := r.client.Get(context.TODO(), scanKey, scan); err != nil {
			logger.Error(err, "Cannot get the scan for the remediation", "ComplianceScan.Name", scan.Name)
			return appliedRemediations, err
		}

		mcfgpools := &mcfgv1.MachineConfigPoolList{}
		err = r.client.List(context.TODO(), mcfgpools)
		if err != nil {
			logger.Error(err, "Cannot list the pools for the remediation")
			return appliedRemediations, err
		}
		// The scans contain a nodeSelector that ultimately must match a machineConfigPool. The only way we can
		// ensure it does is by checking if it matches any MachineConfigPool's labels.
		// See also: https://github.com/openshift/machine-config-operator/blob/master/docs/custom-pools.md
		if !utils.AnyMcfgPoolLabelMatches(scan.Spec.NodeSelector, mcfgpools) {
			logger.Info("Not applying remediation that doesn't have a matching MachineconfigPool", "ComplianceScan.Name", scan.Name)
			// TODO(jaosorior): Add status about remediation not being applicable
			return appliedRemediations, nil
		}

		mc, err := utils.ParseMachineConfig(instance, instance.Spec.Object)
		if err != nil {
			logger.Error(err, "Cannot parse the MachineConfig for the remediation")
			return appliedRemediations, err
		}
		appliedRemediations = append(appliedRemediations, mc)
	}

	return appliedRemediations, nil
}

func getAppliedMcRemediations(r *ReconcileComplianceRemediation, rem *compv1alpha1.ComplianceRemediation) ([]*mcfgv1.MachineConfig, error) {
	var scanSuiteRemediations compv1alpha1.ComplianceRemediationList

	scanSuiteSelector := make(map[string]string)
	scanSuiteSelector[compv1alpha1.SuiteLabel] = rem.Labels[compv1alpha1.SuiteLabel]
	scanSuiteSelector[compv1alpha1.ScanLabel] = rem.Labels[compv1alpha1.ScanLabel]
	scanSuiteSelector[mcfgv1.MachineConfigRoleLabelKey] = rem.Labels[mcfgv1.MachineConfigRoleLabelKey]

	listOpts := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(scanSuiteSelector),
	}

	if err := r.client.List(context.TODO(), &scanSuiteRemediations, &listOpts); err != nil {
		// List should be retried
		return nil, err
	}

	appliedRemediations := make([]*mcfgv1.MachineConfig, 0, len(scanSuiteRemediations.Items))
	for i := range scanSuiteRemediations.Items {
		if !utils.IsMachineConfig(scanSuiteRemediations.Items[i].Spec.Object) {
			continue
		}
		if scanSuiteRemediations.Items[i].Status.ApplicationState != compv1alpha1.RemediationApplied {
			// We'll only merge the one that is being reconciled with those that are already
			// applied
			// TODO: Add a log line with a very high log level
			continue
		}

		if scanSuiteRemediations.Items[i].Name == rem.Name {
			// Won't add the one being reconciled to the list, it might be that we're de-selecting
			// it, so the one being reconciled is handled separately
			// TODO: Add a log line with a very high log level
			continue
		}

		// OK, we've got an applied MC, add it to the list
		mc, err := utils.ParseMachineConfig(&scanSuiteRemediations.Items[i], scanSuiteRemediations.Items[i].Spec.Object)
		if err != nil {
			return nil, err
		}
		appliedRemediations = append(appliedRemediations, mc)
	}

	return appliedRemediations, nil
}

// MergeMachineConfigs combines multiple machineconfig objects into one object.
// It sorts all the configs in increasing order of their name.
// It uses the Ignition config from first object as base and appends all the rest.
// Kernel arguments are concatenated.
// It uses only the OSImageURL provided by the CVO and ignores any MC provided OSImageURL.
//
// taken from MachineConfigOperator
func mergeMachineConfigs(configs []*mcfgv1.MachineConfig, name string, roleLabel string) *mcfgv1.MachineConfig {
	mergedMc := mcfgv1.MergeMachineConfigs(configs, "")

	if mergedMc == nil {
		return nil
	}

	// NOTE(jaosorior): If no version was set (for some reason) lets just add a default
	if mergedMc.Spec.Config.Ignition.Version == "" {
		mergedMc.Spec.Config.Ignition.Version = "2.2.0"
	}

	mergedMc.SetName(name)
	mergedMc.Labels = make(map[string]string)
	mergedMc.Labels[mcfgv1.MachineConfigRoleLabelKey] = roleLabel

	return mergedMc
}

func createOrUpdateMachineConfig(r *ReconcileComplianceRemediation, merged *mcfgv1.MachineConfig, rem *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	mc := &mcfgv1.MachineConfig{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: merged.Name}, mc)
	if err != nil && errors.IsNotFound(err) {
		return createMachineConfig(r, merged, rem, logger)
	} else if err != nil {
		logger.Error(err, "Cannot retrieve MC", "MachineConfig.Name", merged.Name)
		// Get error should be retried
		return err
	}

	if rem.Spec.Apply && mcHasRemediation(mc, rem) {
		// If we have already applied this there's nothing to do
		return nil
	} else if !rem.Spec.Apply && !mcHasRemediation(mc, rem) {
		// If we have already un-applied this there's nothing to do
		return nil
	}
	return updateMachineConfig(r, mc, merged, rem, logger)
}

func deleteMachineConfig(r *ReconcileComplianceRemediation, name string, logger logr.Logger) error {
	mc := &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	err := r.client.Delete(context.TODO(), mc)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("MC to be deleted was already deleted")
		return nil
	} else if err != nil {
		// delete error should be retried
		return err
	}

	return nil
}

func createMachineConfig(r *ReconcileComplianceRemediation, merged *mcfgv1.MachineConfig, rem *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	if rem.Spec.Apply {
		ensureRemediationAnnotationIsSet(merged, rem)
	}
	err := r.client.Create(context.TODO(), merged)
	if err != nil {
		logger.Error(err, "Cannot create MC", "MachineConfig.Name", merged.Name)
		// Create error should be retried
		return err
	}
	logger.Info("MC created", "MachineConfig.Name", merged.Name)
	return nil
}

func updateMachineConfig(r *ReconcileComplianceRemediation, current *mcfgv1.MachineConfig, merged *mcfgv1.MachineConfig, rem *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	mcCopy := current.DeepCopy()
	if rem.Spec.Apply {
		ensureRemediationAnnotationIsSet(mcCopy, rem)
	} else {
		ensureRemediationAnnotationIsNotSet(mcCopy, rem)
	}
	mcCopy.Spec = merged.Spec

	err := r.client.Update(context.TODO(), mcCopy)
	if err != nil {
		logger.Error(err, "Cannot update MC", "MachineConfig.Name", merged.Name)
		// Update should be retried
		return err
	}
	logger.Info("MC updated", "MachineConfig.Name", merged.Name)
	return nil
}

func getRemediationAnnotationKey(remName string) string {
	return utils.DNSLengthName(remediationNameAnnotationKey, remediationNameAnnotationKey+"%s", remName)
}

func ensureRemediationAnnotationIsSet(mc *mcfgv1.MachineConfig, rem *compv1alpha1.ComplianceRemediation) {
	if mc.Annotations == nil {
		mc.Annotations = make(map[string]string)
	}
	mc.Annotations[getRemediationAnnotationKey(rem.Name)] = ""
}

func ensureRemediationAnnotationIsNotSet(mc *mcfgv1.MachineConfig, rem *compv1alpha1.ComplianceRemediation) {
	if mc.Annotations == nil {
		// No need to do anything
		return
	}
	if _, ok := mc.Annotations[getRemediationAnnotationKey(rem.Name)]; ok {
		delete(mc.Annotations, getRemediationAnnotationKey(rem.Name))
	}
}

func mcHasRemediation(mc *mcfgv1.MachineConfig, rem *compv1alpha1.ComplianceRemediation) bool {
	if mc.Annotations == nil {
		return false
	}
	_, ok := mc.Annotations[getRemediationAnnotationKey(rem.Name)]
	return ok
}
