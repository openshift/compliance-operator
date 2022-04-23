package complianceremediation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/go-logr/logr"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	unstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	"github.com/openshift/compliance-operator/pkg/controller/metrics"
)

const ctrlName = "remediationctrl"

var log = logf.Log.WithName(ctrlName)

const (
	remediationNameAnnotationKey = "remediation/"
	defaultDependencyRequeueTime = time.Second * 20
)

// Add creates a new ComplianceRemediation Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, met *metrics.Metrics, _ utils.CtlplaneSchedulingInfo) error {
	return add(mgr, newReconciler(mgr, met))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, met *metrics.Metrics) reconcile.Reconciler {
	return &ReconcileComplianceRemediation{client: mgr.GetClient(), scheme: mgr.GetScheme(),
		recorder: common.NewSafeRecorder(ctrlName, mgr),
		metrics:  met,
	}
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
	scheme   *runtime.Scheme
	recorder record.EventRecorder
	metrics  *metrics.Metrics
}

// Reconcile reads that state of the cluster for a ComplianceRemediation object and makes changes based on the state read
// and what is in the ComplianceRemediation.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileComplianceRemediation) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling ComplianceRemediation")
	// Fetch the ComplianceRemediation instance
	remediationInstance := &compv1alpha1.ComplianceRemediation{}
	getErr := r.client.Get(context.TODO(), request.NamespacedName, remediationInstance)
	if getErr != nil {
		if kerrors.IsNotFound(getErr) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reqLogger.Error(getErr, "Cannot retrieve remediation")
		return reconcile.Result{}, getErr
	}

	if remediationInstance.Spec.Type == "" {
		reqLogger.Info("Updating remediation due to missing type")
		rCopy := remediationInstance.DeepCopy()
		rCopy.Spec.Type = compv1alpha1.ConfigurationRemediation
		if updErr := r.client.Update(context.TODO(), rCopy); updErr != nil {
			// metric remediation error
			return reconcile.Result{}, fmt.Errorf("updating default remediation type: %s", updErr)
		}
		return reconcile.Result{}, nil
	}

	if remediationInstance.Status.ApplicationState == "" {
		reqLogger.Info("Updating remediation due to missing application state")
		rCopy := remediationInstance.DeepCopy()
		rCopy.Status.ApplicationState = compv1alpha1.RemediationPending
		if updErr := r.client.Status().Update(context.TODO(), rCopy); updErr != nil {
			// metric remediation error
			return reconcile.Result{}, fmt.Errorf("updating default remediation application state: %s", updErr)
		}
		r.metrics.IncComplianceRemediationStatus(rCopy.Name, rCopy.Status)
		return reconcile.Result{}, nil
	}
	if isNoLongerOutdated(remediationInstance) {
		reqLogger.Info("Updating remediation cause it's no longer outdated")
		rCopy := remediationInstance.DeepCopy()
		delete(rCopy.Labels, compv1alpha1.OutdatedRemediationLabel)
		updateErr := r.client.Update(context.TODO(), rCopy)
		if updateErr != nil {
			return reconcile.Result{}, fmt.Errorf("removing outdated label: %w", updateErr)
		}
		return reconcile.Result{}, nil
	}

	if remediationInstance.Spec.Current.Object == nil {
		err := fmt.Errorf("No remediation specified. spec.object is empty")
		return common.ReturnWithRetriableError(reqLogger, common.WrapNonRetriableCtrlError(err))
	}

	var reconcileErr error

	if remediationInstance.HasUnmetDependencies() {
		res, depErr := r.handleUnmetDependencies(remediationInstance, reqLogger)
		if res.Requeue || depErr != nil {
			return res, depErr
		}
	}
	if remediationInstance.HasAnnotation(compv1alpha1.RemediationUnsetValueAnnotation) {
		hasUpdate, valueErr := r.handleUnsetValues(remediationInstance, reqLogger)
		if valueErr != nil || hasUpdate {
			return reconcile.Result{}, valueErr
		}
	}
	if remediationInstance.HasAnnotation(compv1alpha1.RemediationValueRequiredAnnotation) {
		hasUpdate, valueReqErr := r.handleValueRequired(remediationInstance, reqLogger)
		if valueReqErr != nil || hasUpdate {
			return reconcile.Result{}, valueReqErr
		}
	}
	//if no UnmetDependencies, UnsetValue, ValueRequired
	if !(remediationInstance.HasUnmetDependencies() || remediationInstance.HasAnnotation(compv1alpha1.RemediationUnsetValueAnnotation) || remediationInstance.HasAnnotation(compv1alpha1.RemediationValueRequiredAnnotation)) {
		reconcileErr = r.reconcileRemediation(remediationInstance, reqLogger)
	}

	// this would have been much nicer with go 1.13 using errors.Is()
	// Only return if the error is retriable. Else, we persist it in the status
	if reconcileErr != nil && common.IsRetriable(reconcileErr) {
		return common.ReturnWithRetriableError(reqLogger, reconcileErr)
	}

	// Second, we'll reconcile the status of the Remediation itself
	statusErr := r.reconcileRemediationStatus(remediationInstance, reqLogger, reconcileErr)
	// this would have been much nicer with go 1.13 using errors.Is()
	if statusErr != nil {
		return common.ReturnWithRetriableError(reqLogger, statusErr)
	}

	if remediationInstance.Spec.Apply && remediationInstance.HasUnmetKubeDependencies() {
		reqLogger.Info("Has unmet kubernetes object dependencies. Requeuing")
		return reconcile.Result{Requeue: true, RequeueAfter: defaultDependencyRequeueTime}, nil
	}
	reqLogger.Info("Done reconciling")
	return reconcile.Result{}, nil
}

// Gets a remediation and ensures the object exists in the cluster if the
// remediation if applicable
func (r *ReconcileComplianceRemediation) reconcileRemediation(instance *compv1alpha1.ComplianceRemediation, logger logr.Logger) error {
	logger.Info("Reconciling remediation")

	obj := getApplicableObject(instance, logger)
	if obj == nil {
		return common.NewNonRetriableCtrlError("Invalid Remediation: No object given")
	}
	if utils.IsMachineConfig(obj) {
		if err := r.verifyAndCompleteMC(obj, instance); err != nil {
			return err
		}
	}
	//verify if the remediation is kubeletconfig, and process it
	if utils.IsKubeletConfig(obj) {
		if err := r.verifyAndCompleteKC(obj, instance); err != nil {
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
				"Please update the compliance-operator's permissions: %w", err)
	} else if runtime.IsNotRegisteredError(err) || meta.IsNoMatchError(err) {
		// If the kind is not available in the cluster, we can't retry
		return common.NewNonRetriableCtrlError(
			"Unable to get fix object for ComplianceRemediation. "+
				"Make sure the CRD is installed: %w", err)
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

	if utils.IsKubeletConfig(remObj) {
		logger.Info("Can't unapply since it is KubeletConfig Remediation")
		return nil
	}

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

func (r *ReconcileComplianceRemediation) handleUnmetDependencies(rem *compv1alpha1.ComplianceRemediation, logger logr.Logger) (reconcile.Result, error) {
	_, hasXccdfDeps := rem.Annotations[compv1alpha1.RemediationDependencyAnnotation]
	_, hasKubeDeps := rem.Annotations[compv1alpha1.RemediationObjectDependencyAnnotation]

	var nMissingDeps int
	if hasXccdfDeps {
		var xccdfDepErr error
		nMissingDeps, xccdfDepErr = r.countXCCDFUnmetDependencies(rem, logger)
		if xccdfDepErr != nil {
			return reconcile.Result{}, xccdfDepErr
		}
	} else if hasKubeDeps {
		var kubeDepErr error
		nMissingDeps, kubeDepErr = r.countKubeUnmetDependencies(rem, logger)
		if kubeDepErr != nil {
			return reconcile.Result{}, kubeDepErr
		}
	} else {
		return reconcile.Result{}, fmt.Errorf("Remediation marked as dependant but no dependencies detected")
	}

	rCopy := rem.DeepCopy()
	labels := rCopy.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	if nMissingDeps > 0 {
		if _, ok := labels[compv1alpha1.RemediationHasUnmetDependenciesLabel]; !ok {
			logger.Info("Labeling remediation to denote it has unmet dependencies")
			labels[compv1alpha1.RemediationHasUnmetDependenciesLabel] = ""
			rCopy.SetLabels(labels)
			err := r.client.Update(context.TODO(), rCopy)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("adding unmet dependencies label: %w", err)
			}
			return reconcile.Result{Requeue: true, RequeueAfter: defaultDependencyRequeueTime}, nil
		}
		return reconcile.Result{}, nil
	}
	logger.Info("Labeling remediation to denote it has all dependencies met")
	logger.Info("Remediation has all its dependencies met", "ComplianceRemediation.Name", rem.Name)
	rCopy.Annotations[compv1alpha1.RemediationDependenciesMetAnnotation] = ""
	delete(rCopy.Labels, compv1alpha1.RemediationHasUnmetDependenciesLabel)
	rCopy.SetLabels(labels)
	err := r.client.Update(context.TODO(), rCopy)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("adding dependencies met annotation: %w", err)
	}

	return reconcile.Result{Requeue: true, RequeueAfter: defaultDependencyRequeueTime}, nil
}

func (r *ReconcileComplianceRemediation) countXCCDFUnmetDependencies(rem *compv1alpha1.ComplianceRemediation, logger logr.Logger) (int, error) {
	var nMissingDeps int
	deps := rem.Annotations[compv1alpha1.RemediationDependencyAnnotation]

	for _, dep := range strings.Split(deps, ",") {
		handled, err := isRemDepHandled(r, rem, dep, logger)
		if err != nil {
			return 0, err
		}

		if !handled {
			logger.Info("Remediation has unmet dependencies, cannot apply", "ComplianceRemediation.Name", rem.Name)
			// Continue so that we can issue all events
			nMissingDeps++
		}
	}
	return nMissingDeps, nil
}

func (r *ReconcileComplianceRemediation) countKubeUnmetDependencies(rem *compv1alpha1.ComplianceRemediation, logger logr.Logger) (int, error) {
	deps, parseErr := rem.ParseRemediationDependencyRefs()

	if parseErr != nil {
		return 0, common.NewNonRetriableCtrlError("error parsing: %w", parseErr)
	}

	var nMissingDeps int
	for _, dep := range deps {
		obj := getObjFromKubeDep(dep)
		key := types.NamespacedName{Name: dep.Name}
		if dep.Namespace != "" {
			key.Namespace = dep.Namespace
		}
		if getErr := r.client.Get(context.TODO(), key, obj); getErr != nil {
			if kerrors.IsNotFound(getErr) || meta.IsNoMatchError(getErr) || runtime.IsNotRegisteredError(getErr) {
				logger.Info("Remediation is missing a kube dependency",
					"APIVersion", dep.APIVersion, "Kind", dep.Kind,
					"Name", dep.Name, "Namespace", dep.Namespace)
				nMissingDeps++
			} else if runtime.IsMissingKind(getErr) || runtime.IsMissingVersion(getErr) {
				return 0, common.NewNonRetriableCtrlError("malformed kube object dependency: %w", getErr)
			} else {
				return 0, fmt.Errorf("error getting kube object dependency: %w", getErr)
			}
		}
	}

	return nMissingDeps, nil
}

func (r *ReconcileComplianceRemediation) handleValueRequired(rem *compv1alpha1.ComplianceRemediation, logger logr.Logger) (bool, error) {
	annotations := rem.GetAnnotations()
	labels := rem.GetLabels()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	requiredValues := annotations[compv1alpha1.RemediationValueRequiredAnnotation]
	requiredValuesList := removeEmptyStrings(strings.Split(requiredValues, ","))
	if len(requiredValuesList) == 0 {
		return false, fmt.Errorf("Error has value-required annotation but empty list")
	}
	_, isRequiredValuesProcessed := labels[compv1alpha1.RemediationValueRequiredProcessedLabel]
	var unsetRequireValueList []string
	var setRequireValueList []string

	//Go through values required list to find is it inside the tailored profile
	for _, requiredValue := range requiredValuesList {
		found, err := r.isRequiredValueSet(rem, requiredValue)
		if err != nil {
			return false, fmt.Errorf("Error finding if required value is set: %v", err)
		}
		if found {
			setRequireValueList = append(setRequireValueList, requiredValue)
		} else {
			unsetRequireValueList = append(unsetRequireValueList, requiredValue)
		}

	}

	currentUnsetValues := removeEmptyStrings(strings.Split(annotations[compv1alpha1.RemediationUnsetValueAnnotation], ","))
	currentUsedValues := removeEmptyStrings(strings.Split(annotations[compv1alpha1.RemediationValueUsedAnnotation], ","))
	if !isRequiredValuesProcessed {
		logger.Info("Updating remediation to denote values have been processed")
		labels[compv1alpha1.RemediationValueRequiredProcessedLabel] = ""
		if len(unsetRequireValueList) > 0 {
			annotations[compv1alpha1.RemediationUnsetValueAnnotation] = strings.Join(append(currentUnsetValues, unsetRequireValueList...), ",")
		}
		if len(setRequireValueList) > 0 {
			annotations[compv1alpha1.RemediationValueUsedAnnotation] = strings.Join(append(currentUsedValues, setRequireValueList...), ",")
		}
		logger.Info("Labeling remediation to denote some values haven't been set")
		rCopy := rem.DeepCopy()
		rCopy.SetAnnotations(annotations)
		rCopy.SetLabels(labels)
		err := r.client.Update(context.TODO(), rCopy)
		if err != nil {
			return false, fmt.Errorf("adding xccdf required-value label/annotation: %w", err)
		}
		return true, nil
	}
	return false, nil
}

//To find if it is set in tailored profile
func (r *ReconcileComplianceRemediation) isRequiredValueSet(rem *compv1alpha1.ComplianceRemediation, requiredValue string) (bool, error) {
	var scan = &compv1alpha1.ComplianceScan{}
	err := r.client.Get(context.TODO(), types.NamespacedName{
		Namespace: rem.GetNamespace(),
		Name:      rem.GetScan(),
	}, scan)

	if err != nil {
		return false, err //error getting scan
	}

	if scan.Spec.TailoringConfigMap != nil {
		tpcm := &corev1.ConfigMap{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: scan.Spec.TailoringConfigMap.Name, Namespace: rem.GetNamespace()}, tpcm)
		if err != nil {
			return false, err
		}
		tpContent, ok := tpcm.Data["tailoring.xml"]
		if !ok {
			return false, fmt.Errorf("Error retriving Tailor Profile CM")
		}
		if strings.Contains(tpContent, strings.ReplaceAll(requiredValue, "-", "_")) {
			return true, nil
		}
		return false, nil
	}
	//scan did not have a tailored profile
	return false, nil
}

func (r *ReconcileComplianceRemediation) handleUnsetValues(rem *compv1alpha1.ComplianceRemediation, logger logr.Logger) (bool, error) {
	nNotSetValues := r.countXCCDFUnsetValues(rem)
	if nNotSetValues == 0 {
		return false, fmt.Errorf("Error empty unset-value list")
	}
	labels := rem.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	//To avoide setting incorrect label for the one that has required-value annotation but does not have any un-set values
	if nNotSetValues > 0 {
		//Set unset lable if labes has not been set
		if _, ok := labels[compv1alpha1.RemediationUnsetValueLabel]; !ok {
			labels[compv1alpha1.RemediationUnsetValueLabel] = ""
			rCopy := rem.DeepCopy()
			rCopy.SetLabels(labels)
			err := r.client.Update(context.TODO(), rCopy)
			if err != nil {
				return false, fmt.Errorf("adding un-set xccdf value label: %w", err)
			}
			return true, nil
			//return true to notify caller that we updated the object
		}
		return false, nil
	}
	return false, nil
}

func (r *ReconcileComplianceRemediation) countXCCDFUnsetValues(rem *compv1alpha1.ComplianceRemediation) int {
	notSetValues := rem.Annotations[compv1alpha1.RemediationUnsetValueAnnotation]
	if notSetValues == "" {
		return 0
	}
	return len(removeEmptyStrings(strings.Split(notSetValues, ",")))
}

func removeEmptyStrings(s []string) []string {
	var resultString []string
	for _, str := range s {
		if str == "" {
			continue
		}
		resultString = append(resultString, str)
	}
	return resultString
}

func getObjFromKubeDep(dep compv1alpha1.RemediationObjectDependencyReference) runtime.Object {
	obj := &unstructured.Unstructured{}
	obj.SetKind(dep.Kind)
	obj.SetAPIVersion(dep.APIVersion)
	return obj
}

func isRemDepHandled(r *ReconcileComplianceRemediation, rem *compv1alpha1.ComplianceRemediation, checkId string, logger logr.Logger) (bool, error) {
	var checkList compv1alpha1.ComplianceCheckResultList

	err := r.client.List(context.TODO(), &checkList, client.MatchingFields{"id": checkId})
	if err != nil {
		logger.Error(err, "Could not list checks by ID", compv1alpha1.ComplianceRemediationDependencyField, checkId)
		return false, err
	}

	if len(checkList.Items) == 0 {
		r.recorder.Eventf(rem, corev1.EventTypeWarning, "RemediationDependencyCannotBeMet",
			"The marked dependency %s is missing and cannot be met as it's not part of the benchmark.", checkId)
		logger.Info("Missing dependency cannot be satisfied", "ComplianceCheckResult.Name", checkId, "ComplianceRemediation.Name", rem.Name)
		return false, nil
	}

	for _, check := range checkList.Items {
		switch check.Status {
		case compv1alpha1.CheckResultPass:
			logger.Info("Dependency satisfied", "ComplianceCheckResult.Name", check.Name, "ComplianceRemediation.Name", rem.Name)
			return true, nil
		case compv1alpha1.CheckResultInfo, compv1alpha1.CheckResultFail:
			// in general this should not be the case and infos should be standalone, but if it is, we should probably treat it like fail
			logger.Info("Dependency not yet satisfied", "ComplianceCheckResult.Name", check.Name, "ComplianceRemediation.Name", rem.Name)
			r.recorder.Eventf(rem, corev1.EventTypeNormal, "RemediationDependencyCannotBeMet",
				"The dependency %s not met, please apply its remediations and retry", check.Name)
			return false, nil
		default:
			r.recorder.Eventf(rem, corev1.EventTypeWarning, "RemediationDependencyCannotBeMet",
				"The dependency %s cannot be met with status %s", check.Name, check.Status)
			logger.Info("Dependency cannot be satisfied", "ComplianceCheckResult.Name", check.Name, "ComplianceRemediation.Name", rem.Name)
			return false, nil
		}
	}
	return true, nil
}

func (r *ReconcileComplianceRemediation) reconcileRemediationStatus(instance *compv1alpha1.ComplianceRemediation,
	logger logr.Logger, errorApplying error) error {
	instanceCopy := instance.DeepCopy()
	logger.Info("Updating status of remediation")
	r.setRemediationStatus(instanceCopy, errorApplying, logger)

	if err := r.client.Status().Update(context.TODO(), instanceCopy); err != nil {
		// metric remediation error
		logger.Error(err, "Failed to update the remediation status")
		// This should be retried
		return err
	}
	r.metrics.IncComplianceRemediationStatus(instanceCopy.Name, instanceCopy.Status)

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
	if ok, _ := utils.AnyMcfgPoolLabelMatches(scan.Spec.NodeSelector, mcfgpools); !ok {
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

// Process kubeletconfig remediation
func (r *ReconcileComplianceRemediation) verifyAndCompleteKC(obj *unstructured.Unstructured, rem *compv1alpha1.ComplianceRemediation) error {
	scan := &compv1alpha1.ComplianceScan{}
	scanKey := types.NamespacedName{Name: rem.Labels[compv1alpha1.ComplianceScanLabel], Namespace: rem.Namespace}
	if err := r.client.Get(context.TODO(), scanKey, scan); err != nil {
		return fmt.Errorf("couldn't get scan for KC remediation: %w", err)
	}
	mcfgpools := &mcfgv1.MachineConfigPoolList{}
	if err := r.client.List(context.TODO(), mcfgpools); err != nil {
		return fmt.Errorf("couldn't list the pools for the remediation: %w", err)
	}
	// The scans contain a nodeSelector that ultimately must match a machineConfigPool. The only way we can
	// ensure it does is by checking if it matches any MachineConfigPool's labels.
	// See also: https://github.com/openshift/machine-config-operator/blob/master/docs/custom-pools.md
	ok, pool := utils.AnyMcfgPoolLabelMatches(scan.Spec.NodeSelector, mcfgpools)
	if !ok {
		return common.NewNonRetriableCtrlError("not applying remediation that doesn't have a matching MachineconfigPool. Scan: %s", scan.Name)
	}

	// We need to check if there is exsisting kubeletConfigMC present for selected MCP
	hasCustomKC, kubeletMCName, err := utils.IsMcfgPoolUsingKC(pool)

	if err != nil {
		return err
	}
	// we need to patch the remediation if there is already a CustomKC present
	if hasCustomKC {

		kubeletMC := &mcfgv1.MachineConfig{}
		kMCKey := types.NamespacedName{Name: kubeletMCName}

		if err := r.client.Get(context.TODO(), kMCKey, kubeletMC); err != nil {
			return fmt.Errorf("couldn't get current generated KubeletConfig MC: %w", err)
		}
		// We need to get name of original kubelet config that used to generate this kubeletconfig machine config
		// if we can't find owner of generated mc, we will create custom kubeletconfig instead
		kubeletConfig, err := utils.GetKCFromMC(kubeletMC, r.client)
		if err != nil {
			return fmt.Errorf("couldn't get kubelet config from machine config: %w", err)
		}
		// Set kubelet config name
		obj.SetName(kubeletConfig.GetName())
		obj.SetLabels(kubeletConfig.GetLabels())
		return nil

	}

	// We will need to create a kubelet config if there is no custom KC
	kubeletName := "compliance-operator-kubelet-" + pool.GetName()

	// Set kubelet config name
	obj.SetName(kubeletName)

	// Set MachineConfigSelector
	NodeSelector := []string{"spec", "machineConfigPoolSelector", "matchLabels"}

	machineConfigPoolSelector := map[string]string{"pools.operator.machineconfiguration.openshift.io/" + pool.GetName(): ""}
	err = unstructured.SetNestedStringMap(obj.Object, machineConfigPoolSelector, NodeSelector...)
	if err != nil {
		return fmt.Errorf("couldn't set machineConfigPoolSelector for kubeletconfig: %w", err)
	}
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

// Returns whether the remediation used to be outdated, but no longer is.
func isNoLongerOutdated(r *compv1alpha1.ComplianceRemediation) bool {
	labels := r.GetLabels()
	if labels == nil {
		return false
	}
	_, ok := labels[compv1alpha1.OutdatedRemediationLabel]
	if !ok {
		return false
	}

	return r.Spec.Outdated.Object == nil
}

func (r *ReconcileComplianceRemediation) setRemediationStatus(rem *compv1alpha1.ComplianceRemediation, errorApplying error, logger logr.Logger) {
	if errorApplying != nil {
		if wasErrorOnOptionalRemediation(rem, errorApplying) {
			logger.Info("Optional remediation couldn't be applied")
			rem.Status.ApplicationState = compv1alpha1.RemediationNotApplied
			r.recorder.Eventf(rem, corev1.EventTypeWarning, "OptionalDependencyNotApplied",
				"Optional remediation couldn't be applied: %s", errorApplying)
		} else {
			logger.Info("Remediation had an error")
			rem.Status.ApplicationState = compv1alpha1.RemediationError
		}
		rem.Status.ErrorMessage = errorApplying.Error()
		return
	}

	if !rem.Spec.Apply {
		logger.Info("Remediation will now be unapplied")
		rem.Status.ApplicationState = compv1alpha1.RemediationNotApplied
		return
	}

	if rem.Spec.Outdated.Object != nil {
		logger.Info("Remediation remains outdated")
		rem.Status.ApplicationState = compv1alpha1.RemediationOutdated
		return
	}

	if rem.HasUnmetDependencies() {
		logger.Info("Remediation has un-met dependencies.")
		rem.Status.ApplicationState = compv1alpha1.RemediationMissingDependencies
		return
	}

	if rem.HasAnnotation(compv1alpha1.RemediationUnsetValueAnnotation) {
		logger.Info("Remediation has un-set values.")
		rem.Status.ApplicationState = compv1alpha1.RemediationNeedsReview
		return
	}

	logger.Info("Remediation will now be applied")
	rem.Status.ApplicationState = compv1alpha1.RemediationApplied
}

func wasErrorOnOptionalRemediation(r *compv1alpha1.ComplianceRemediation, errorApplying error) bool {
	annotations := r.GetAnnotations()
	// This wasn't an optional remediation. That's represented through
	// an annotation
	if annotations == nil {
		return false
	}

	if _, ok := annotations[compv1alpha1.RemediationOptionalAnnotation]; !ok {
		return false
	}

	wrapped := errors.Unwrap(errorApplying)
	if wrapped == nil {
		return false
	}

	return runtime.IsNotRegisteredError(wrapped) || meta.IsNoMatchError(wrapped)
}
