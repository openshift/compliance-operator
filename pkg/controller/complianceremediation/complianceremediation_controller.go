package complianceremediation

import (
	"context"
	"fmt"
	"sort"

	ign "github.com/coreos/ignition/config/v2_2"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	mcfgClient "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned/typed/machineconfiguration.openshift.io/v1"
)

var log = logf.Log.WithName("controller_complianceremediation")

// Add creates a new ComplianceRemediation Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	// FIXME: Should we rather move the initialization into a method that does lazy-init on first use
	// from the reconcile loop?
	mcClient, err := mcfgClient.NewForConfig(mgr.GetConfig())
	if err != nil {
		return &ReconcileComplianceRemediation{}
	}

	return &ReconcileComplianceRemediation{client: mgr.GetClient(),
		mcClient: mcClient,
		scheme:   mgr.GetScheme()}
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
	client   client.Client
	mcClient *mcfgClient.MachineconfigurationV1Client
	scheme   *runtime.Scheme
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
		return reconcile.Result{}, err
	}

	// First, we'll reconcile the MC that is the result of applying Remediations
	switch remediationInstance.Spec.Type {
	case compv1alpha1.McRemediation:
		reqLogger.Info("Reconciling a MachineConfig remediation")
		err = r.reconcileMcRemediation(remediationInstance, reqLogger)
	default:
		err = fmt.Errorf("unsupported remediation type %s", remediationInstance.Spec.Type)
	}

	// this would have been much nicer with go 1.13 using errors.Is()
	if err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	// Second, we'll reconcile the status of the Remediation itself
	err = r.reconcileRemediationStatus(remediationInstance, reqLogger)
	// this would have been much nicer with go 1.13 using errors.Is()
	if err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	reqLogger.Info("Done reconciling")
	return reconcile.Result{}, nil
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

	logger.Info("Will create or update MC", "name", name)
	mergedMc := mergeMachineConfigs(mcList, name, instance.Labels[mcfgv1.MachineConfigRoleLabelKey])

	// if the mergedMc was nil, then we should remove the resulting MC, probably the last selected
	// remediation was deselected
	if mergedMc == nil {
		logger.Info("The merged MC was nil, will delete the old MC", "oldMc", name)
		return deleteMachineConfig(r, name, logger)
	}

	// Actually touch the MC, this hands over control to the MCO
	logger.Info("Merged MC", "mc", mergedMc)
	if err := createOrUpdateMachineConfig(r, mergedMc, logger); err != nil {
		logger.Error(err, "Failed to create or modify the MC")
		// The err itself is already retriable (or not)
		return err
	}

	return nil
}

func (r *ReconcileComplianceRemediation) reconcileRemediationStatus(instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	instanceCopy := instance.DeepCopy()
	if instance.Spec.Apply {
		instanceCopy.Status.ApplicationState = compv1alpha1.RemediationApplied
	} else {
		instanceCopy.Status.ApplicationState = compv1alpha1.RemediationNotSelected
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

	// If the one being reconciled is supposed to be applied as well, add it to the list
	if instance.Spec.Apply == true {
		appliedRemediations = append(appliedRemediations, &instance.Spec.MachineConfigContents)
	}

	logger.Info("Adding content", "contents", appliedRemediations)

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
		if scanSuiteRemediations.Items[i].Spec.Type != compv1alpha1.McRemediation {
			// We'll only merge MachineConfigs
			continue
		}
		if scanSuiteRemediations.Items[i].Status.ApplicationState != compv1alpha1.RemediationApplied {
			// We'll only merge the one that is being reconciled with those that are already
			// applied
			continue
		}

		if scanSuiteRemediations.Items[i].Name == rem.Name {
			// Won't add the one being reconciled to the list, it might be that we're de-selecting
			// it, so the one being reconciled is handled separately
			continue
		}

		// OK, we've got an applied MC, add it to the list
		appliedRemediations = append(appliedRemediations, &scanSuiteRemediations.Items[i].Spec.MachineConfigContents)
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
	if len(configs) == 0 {
		return nil
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].Name < configs[j].Name })

	var fips bool
	outIgn := configs[0].Spec.Config
	for idx := 1; idx < len(configs); idx++ {
		// if any of the config has FIPS enabled, it'll be set
		if configs[idx].Spec.FIPS {
			fips = true
		}
		outIgn = ign.Append(outIgn, configs[idx].Spec.Config)
	}
	kargs := []string{}
	for _, cfg := range configs {
		kargs = append(kargs, cfg.Spec.KernelArguments...)
	}
	mergedMc := &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: mcfgv1.MachineConfigSpec{
			OSImageURL:      "",
			KernelArguments: kargs,
			Config:          outIgn,
			FIPS:            fips,
		},
	}

	mergedMc.Labels = make(map[string]string)
	mergedMc.Labels[mcfgv1.MachineConfigRoleLabelKey] = roleLabel

	return mergedMc
}

func createOrUpdateMachineConfig(r *ReconcileComplianceRemediation, merged *mcfgv1.MachineConfig, logger logr.Logger) error {
	mc, err := r.mcClient.MachineConfigs().Get(merged.Name, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		return createMachineConfig(r, merged, logger)
	} else if err != nil {
		logger.Error(err, "Cannot retrieve MC", "MC", merged.Name)
		// Get error should be retried
		return err
	}

	return updateMachineConfig(r, mc, merged, logger)
}

func deleteMachineConfig(r *ReconcileComplianceRemediation, name string, logger logr.Logger) error {
	err := r.mcClient.MachineConfigs().Delete(name, &metav1.DeleteOptions{})
	if err != nil && errors.IsNotFound(err) {
		logger.Info("MC to be deleted was already deleted")
		return nil
	} else if err != nil {
		// delete error should be retried
		return err
	}

	return nil
}

func createMachineConfig(r *ReconcileComplianceRemediation, merged *mcfgv1.MachineConfig, logger logr.Logger) error {
	_, err := r.mcClient.MachineConfigs().Create(merged)
	if err != nil {
		logger.Error(err, "Cannot create MC", "mc name", merged.Name)
		// Create error should be retried
		return err
	}
	logger.Info("MC created", "mc name", merged.Name)
	return nil
}

func updateMachineConfig(r *ReconcileComplianceRemediation, current *mcfgv1.MachineConfig, merged *mcfgv1.MachineConfig, logger logr.Logger) error {
	mcCopy := current.DeepCopy()
	mcCopy.Spec = merged.Spec

	_, err := r.mcClient.MachineConfigs().Update(mcCopy)
	if err != nil {
		logger.Error(err, "Cannot update MC", "mc name", merged.Name)
		// Update should be retried
		return err
	}
	logger.Info("MC updated", "mc name", merged.Name)
	return nil
}
