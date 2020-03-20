package compliancesuite

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
)

var log = logf.Log.WithName("suitectrl")

// Add creates a new ComplianceSuite Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileComplianceSuite{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("compliancesuite-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource ComplianceSuite
	err = c.Watch(&source.Kind{Type: &compv1alpha1.ComplianceSuite{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource ComplianceScans and requeue the owner ComplianceSuite
	err = c.Watch(&source.Kind{Type: &compv1alpha1.ComplianceScan{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &compv1alpha1.ComplianceSuite{},
	})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource ComplianceRemediation and requeue the owner ComplianceSuite
	err = c.Watch(&source.Kind{Type: &compv1alpha1.ComplianceRemediation{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &compv1alpha1.ComplianceSuite{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileComplianceSuite implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileComplianceSuite{}

// ReconcileComplianceSuite reconciles a ComplianceSuite object
type ReconcileComplianceSuite struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a ComplianceSuite object and makes changes based on the state read
// and what is in the ComplianceSuite.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileComplianceSuite) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling ComplianceSuite")

	// Fetch the ComplianceSuite suite
	suite := &compv1alpha1.ComplianceSuite{}
	err := r.client.Get(context.TODO(), request.NamespacedName, suite)
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
	reqLogger.Info("Retrieved suite", "suite", suite)

	suiteCopy := suite.DeepCopy()
	if err := r.reconcileScans(suiteCopy, reqLogger); err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	if err := r.reconcileRemediations(suiteCopy, reqLogger); err != nil {
		return common.ReturnWithRetriableError(reqLogger, err)
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileComplianceSuite) reconcileScans(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	for _, scanWrap := range suite.Spec.Scans {
		// The scans contain a nodeSelector that ultimately must match a machineConfigPool. The only way we can
		// ensure it does is by checking if the nodeSelector contains a label with the key
		//"node-role.kubernetes.io/XXX". Then the matching Pool would be labeled with
		//"machineconfiguration.openshift.io/role: XXX".
		//See also: https://github.com/openshift/machine-config-operator/blob/master/docs/custom-pools.md
		if utils.GetFirstNodeRole(scanWrap.NodeSelector) == "" {
			logger.Info("Not scheduling scan without a role label", "scan", scanWrap.Name)
			continue
		}

		scan := &compv1alpha1.ComplianceScan{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: scanWrap.Name, Namespace: suite.Namespace}, scan)
		if err != nil && errors.IsNotFound(err) {
			// If the scan was not found, launch it
			logger.Info("Scan not found, launching..", "scan", scanWrap.Name)
			if err = launchScanForSuite(r, suite, &scanWrap, logger); err != nil {
				return err
			}
			logger.Info("Scan created", "scan", scanWrap.Name)
			// No point in reconciling status yet
			continue
		} else if err != nil {
			return err
		}

		// The scan already exists, let's just make sure its status is reflected
		if err := r.reconcileScanStatus(suite, scan, logger); err != nil {
			return err
		}
	}

	return nil
}

func (r *ReconcileComplianceSuite) reconcileScanStatus(suite *compv1alpha1.ComplianceSuite, scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	// See if we already have a ScanStatusWrapper for this name
	for idx, scanStatusWrap := range suite.Status.ScanStatuses {
		if scan.Name == scanStatusWrap.Name {
			logger.Info("About to update scan status", "scan", scan.Name)
			err := r.updateScanStatus(suite, idx, &scanStatusWrap, scan, logger)
			if err != nil {
				logger.Error(err, "Could not update scan status")
				return err
			}
			return nil
		}
	}

	logger.Info("About to add scan status", "scan", scan.Name)
	err := r.addScanStatus(suite, scan, logger)
	if err != nil {
		logger.Error(err, "Could not add scan status")
		return err
	}
	return nil
}

// updates the status of a scan in the compliance suite. Note that the suite that this takes is already a copy, so it's safe to modify
func (r *ReconcileComplianceSuite) updateScanStatus(suite *compv1alpha1.ComplianceSuite, idx int, scanStatusWrap *compv1alpha1.ComplianceScanStatusWrapper, scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	// if yes, update it, if the status differs
	if scanStatusWrap.Phase == scan.Status.Phase {
		logger.Info("Not updating scan, the phase is the same", "scan", scanStatusWrap.Name, "phase", scanStatusWrap.Phase)
		return nil
	}

	modScanStatus := compv1alpha1.ComplianceScanStatusWrapper{
		ComplianceScanStatus: compv1alpha1.ComplianceScanStatus{
			Phase: scan.Status.Phase,
		},
		Name: scan.Name,
	}

	if scan.Status.Phase == compv1alpha1.PhaseDone {
		modScanStatus.Result = scan.Status.Result
		modScanStatus.ErrorMessage = scan.Status.ErrorMessage
	}

	// Replace the copy so we use fresh metadata
	suite = suite.DeepCopy()
	suite.Status.ScanStatuses[idx] = modScanStatus
	logger.Info("Updating scan status", "scan", modScanStatus.Name, "phase", modScanStatus.Phase)

	return r.client.Status().Update(context.TODO(), suite)
}

func (r *ReconcileComplianceSuite) addScanStatus(suite *compv1alpha1.ComplianceSuite, scan *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	// if not, create the scan status with the name and the current state
	newScanStatus := compv1alpha1.ComplianceScanStatusWrapper{
		ComplianceScanStatus: compv1alpha1.ComplianceScanStatus{
			Phase: scan.Status.Phase,
		},
		Name: scan.Name,
	}

	// Replace the copy so we use fresh metadata
	suite = suite.DeepCopy()
	suite.Status.ScanStatuses = append(suite.Status.ScanStatuses, newScanStatus)
	logger.Info("Adding scan status", "scan", newScanStatus.Name, "phase", newScanStatus.Phase)
	return r.client.Status().Update(context.TODO(), suite)
}

func launchScanForSuite(r *ReconcileComplianceSuite, suite *compv1alpha1.ComplianceSuite, scanWrap *compv1alpha1.ComplianceScanSpecWrapper, logger logr.Logger) error {
	scan := newScanForSuite(suite, scanWrap)
	if scan == nil {
		return fmt.Errorf("cannot create ComplianceScan for %s:%s", suite.Name, scanWrap.Name)
	}

	if err := controllerutil.SetControllerReference(suite, scan, r.scheme); err != nil {
		log.Error(err, "Failed to set scan ownership", "scan", scan)
		return err
	}

	logger.Info("About to launch scan", "scan", scan)
	err := r.client.Create(context.TODO(), scan)
	if errors.IsAlreadyExists(err) {
		// Was there a race that created the scan in the meantime?
		return nil
	} else if err != nil {
		return err
	}

	return nil
}

func newScanForSuite(suite *compv1alpha1.ComplianceSuite, scanWrap *compv1alpha1.ComplianceScanSpecWrapper) *compv1alpha1.ComplianceScan {
	scanLabels := map[string]string{
		"compliancesuite": suite.Name,
	}

	return &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanWrap.Name,
			Namespace: suite.Namespace,
			Labels:    scanLabels,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			ContentImage: scanWrap.ContentImage,
			Profile:      scanWrap.Profile,
			Rule:         scanWrap.Rule,
			Content:      scanWrap.Content,
			NodeSelector: scanWrap.NodeSelector,
			Debug:        scanWrap.Debug,
		},
	}
}

// Reconcile the remediation statuses in the suite. Note that the suite that this takes is already
// a copy, so it's safe to modify.
func (r *ReconcileComplianceSuite) reconcileRemediations(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	// Get all the remediations
	var remList compv1alpha1.ComplianceRemediationList
	listOpts := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{"complianceoperator.openshift.io/suite": suite.Name}),
	}

	if err := r.client.List(context.TODO(), &remList, &listOpts); err != nil {
		return err
	}

	// Construct the list of the statuses
	remOverview := make([]compv1alpha1.ComplianceRemediationNameStatus, len(remList.Items))
	for idx, rem := range remList.Items {
		remOverview[idx].ScanName = rem.Labels[compv1alpha1.ScanLabel]
		remOverview[idx].RemediationName = rem.Name
		remOverview[idx].Type = rem.Spec.Type
		remOverview[idx].Apply = rem.Spec.Apply
	}

	// Replace the copy so we use fresh metadata
	suite = suite.DeepCopy()
	// Make sure we don't try to use the value as-is if it's nil
	if suite.Status.ScanStatuses == nil {
		suite.Status.ScanStatuses = []compv1alpha1.ComplianceScanStatusWrapper{}
	}
	suite.Status.RemediationOverview = remOverview
	logger.Info("Remediations", "remOverview", remOverview)
	return r.client.Status().Update(context.TODO(), suite)
}
