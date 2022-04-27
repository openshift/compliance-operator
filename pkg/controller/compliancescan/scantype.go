package compliancescan

import (
	"context"
	goerrors "errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type scanTypeHandler interface {
	validate() (bool, error)
	getScan() *compv1alpha1.ComplianceScan
	createScanWorkload() error
	handleRunningScan() (bool, error)
	// shouldLaunchAggregator is a check that tests whether the scanner already failed
	// hard in which case there might not be a reason to launch the aggregator pod, e.g.
	// in cases the content cannot be loaded at all
	shouldLaunchAggregator() (bool, string, error)

	// gatherResults will iterate the nodes in the scan and get the results
	// for the OpenSCAP check. If the results haven't yet been persisted in
	// the relevant ConfigMap, the a requeue will be requested since the
	// results are not ready.
	gatherResults() (compv1alpha1.ComplianceScanStatusResult, bool, error)
	cleanup() error
}

func getScanTypeHandler(r *ReconcileComplianceScan, scan *compv1alpha1.ComplianceScan, logger logr.Logger) (scanTypeHandler, error) {
	scantype, err := scan.GetScanTypeIfValid()
	if err != nil {
		return nil, err
	}
	switch scantype {
	case compv1alpha1.ScanTypePlatform:
		return newPlatformScanTypeHandler(r, scan, logger)
	case compv1alpha1.ScanTypeNode:
		return newNodeScanTypeHandler(r, scan, logger)
	}
	return nil, nil
}

type nodeScanTypeHandler struct {
	r     *ReconcileComplianceScan
	scan  *compv1alpha1.ComplianceScan
	l     logr.Logger
	nodes []corev1.Node
}

// newNodeScanTypeHandler creates a new instance of a scanTypeHandler.
// Note that it assumes that the scan instance that's given is already a copy.
func newNodeScanTypeHandler(r *ReconcileComplianceScan, scan *compv1alpha1.ComplianceScan, logger logr.Logger) (scanTypeHandler, error) {
	nh := &nodeScanTypeHandler{
		r:    r,
		scan: scan,
		l:    logger,
	}

	nodes, err := nh.getTargetNodes()
	if err != nil {
		nh.l.Error(err, "Cannot get nodes")
		return nil, err
	}
	nh.nodes = nodes
	return nh, nil
}

func (nh *nodeScanTypeHandler) getScan() *compv1alpha1.ComplianceScan {
	return nh.scan
}

func (nh *nodeScanTypeHandler) getTargetNodes() ([]corev1.Node, error) {
	var nodes corev1.NodeList

	switch nh.scan.GetScanType() {
	case compv1alpha1.ScanTypePlatform:
		return nodes.Items, nil // Nodes are only relevant to the node scan type. Return the empty node list otherwise.
	case compv1alpha1.ScanTypeNode:
		// we only scan Linux nodes
		nodeScanSelector := map[string]string{"kubernetes.io/os": "linux"}
		listOpts := client.ListOptions{
			LabelSelector: labels.SelectorFromSet(labels.Merge(nh.scan.Spec.NodeSelector, nodeScanSelector)),
		}

		if err := nh.r.client.List(context.TODO(), &nodes, &listOpts); err != nil {
			return nodes.Items, err
		}
	}

	return nodes.Items, nil
}

func (nh *nodeScanTypeHandler) validate() (bool, error) {
	if len(nh.nodes) == 0 {
		warning := "No nodes matched the nodeSelector"
		nh.l.Info(warning)
		nh.r.recorder.Event(nh.scan, corev1.EventTypeWarning, "NoMatchingNodes", warning)
		instanceCopy := nh.scan.DeepCopy()
		instanceCopy.Status.Result = compv1alpha1.ResultNotApplicable
		instanceCopy.Status.Phase = compv1alpha1.PhaseDone
		err := nh.r.updateStatusWithEvent(instanceCopy, nh.l)
		return false, err
	}
	nodeWarning := "Not continuing scan: Node is unschedulable"
	for idx := range nh.nodes {
		node := &nh.nodes[idx]
		// Surface error if we're being strict with our node scans
		if nh.getScan().IsStrictNodeScan() && node.Spec.Unschedulable {
			nh.l.Info(nodeWarning, "Node.Name", node.GetName())
			eventFmt := fmt.Sprintf("%s: %s", nodeWarning, node.GetName())
			nh.r.recorder.Event(nh.scan, corev1.EventTypeWarning, "UnschedulableNode", eventFmt)
			return false, nil
		}
	}

	return true, nil
}

func (nh *nodeScanTypeHandler) createScanWorkload() error {
	// On each eligible node..
	for idx := range nh.nodes {
		node := &nh.nodes[idx]
		// ..schedule a pod..
		nh.l.Info("Creating a pod for node", "Pod.Name", node.Name)
		pod := newScanPodForNode(nh.scan, node, nh.l)
		if err := nh.r.launchScanPod(nh.scan, pod, nh.l); err != nil {
			return err
		}
	}

	return nil
}

func (nh *nodeScanTypeHandler) handleRunningScan() (bool, error) {
	for idx := range nh.nodes {
		node := &nh.nodes[idx]
		var unschedulableErr *podUnschedulableError
		running, err := isPodRunningInNode(nh.r, nh.scan, node, nh.l)
		if errors.IsNotFound(err) {
			// Let's go back to the previous state and make sure all the nodes are covered.
			nh.l.Info("Phase: Running: A pod is missing. Going to state LAUNCHING to make sure we launch it",
				"compliancescan", nh.scan.ObjectMeta.Name, "node", node.Name)
			nh.scan.Status.Phase = compv1alpha1.PhaseLaunching
			err = nh.r.client.Status().Update(context.TODO(), nh.scan)
			if err != nil {
				return true, err
			}
			return true, nil
		} else if goerrors.As(err, &unschedulableErr) {
			// Create custom error message for this pod that couldn't be scheduled
			cmName := getConfigMapForNodeName(nh.scan.Name, node.Name)
			errorReader := strings.NewReader(err.Error())
			cm := utils.GetResultConfigMap(nh.scan, cmName, "error-msg", node.Name,
				errorReader, false, common.PodUnschedulableExitCode, "")
			cmKey := types.NamespacedName{Name: cm.Name, Namespace: cm.Namespace}
			foundcm := corev1.ConfigMap{}
			cmGetErr := nh.r.client.Get(context.TODO(), cmKey, &foundcm)

			if errors.IsNotFound(cmGetErr) {
				if cmCreateErr := nh.r.client.Create(context.TODO(), cm); cmCreateErr != nil {
					if !errors.IsAlreadyExists(cmCreateErr) {
						return true, cmCreateErr
					}
				}
			} else if cmGetErr != nil {
				return true, cmGetErr
			}

			// We're good, the CM that tells us about this error is already there
			// let's continue to check the next pod
		} else if err != nil {
			return true, err
		}
		if running {
			return true, nil
		}
	}
	return false, nil
}

func (nh *nodeScanTypeHandler) shouldLaunchAggregator() (bool, string, error) {
	var warnings string
	for _, node := range nh.nodes {
		foundCM, err := getNodeScanCM(nh.r, nh.scan, node.Name)

		// Could be a transient error, so we requeue if there's any
		// error here.
		if err != nil {
			return false, "", nil
		}

		warns, ok := foundCM.Data["warnings"]
		if ok {
			warnings = warns
		}

		// NOTE: err is only set if there is an error in the scan run
		err = checkScanUnknownError(foundCM)
		if err != nil {
			return true, warnings, err
		}
	}
	return true, warnings, nil
}

func (nh *nodeScanTypeHandler) gatherResults() (compv1alpha1.ComplianceScanStatusResult, bool, error) {
	var lastNonCompliance compv1alpha1.ComplianceScanStatusResult
	var result compv1alpha1.ComplianceScanStatusResult
	compliant := true
	isReady := true

	for _, node := range nh.nodes {
		foundCM, err := getNodeScanCM(nh.r, nh.scan, node.Name)

		// Could be a transient error, so we requeue if there's any
		// error here. Note that we don't persist the error
		if err != nil {
			nh.l.Info("Node has no result ConfigMap yet", "node.Name", node.Name)
			isReady = false
			continue
		}

		cmHasResult := scanResultReady(foundCM)
		if cmHasResult == false {
			nh.l.Info("Scan results not ready, retrying. If the issue persists, restart or recreate the scan",
				"ComplianceScan.Name", nh.scan.Name)
			isReady = false
			continue
		}

		// NOTE: err is only set if there is an error in the scan run
		result, err = getScanResult(foundCM)

		// we output the last result if it was an error
		if result == compv1alpha1.ResultError {
			if !nh.getScan().IsStrictNodeScan() {
				errCode := foundCM.Data["exit-code"]
				// If the pod was unschedulable and the scan is not
				// strict, we can skip the error
				if errCode == common.PodUnschedulableExitCode {
					skipWarn := "Skipping result for scan: Node is unschedulable"
					nh.l.Info(skipWarn, "Node.Name", node.GetName())
					eventFmt := fmt.Sprintf("%s: %s", skipWarn, node.GetName())
					nh.r.recorder.Event(nh.scan, corev1.EventTypeWarning, "UnschedulableNode", eventFmt)
					continue
				}
			}
			nh.l.Info("Node scan error", "node.Name", node.Name, "errMsg", err)
			return result, true, err
		}
		// Store the last non-compliance, so we can output that if
		// there were no errors.
		if result == compv1alpha1.ResultNonCompliant {
			lastNonCompliance = result
			compliant = false
		}
	}

	if !compliant {
		return lastNonCompliance, isReady, nil
	}

	return result, isReady, nil
}

func (nh *nodeScanTypeHandler) cleanup() error {
	nh.l.Info("Deleting node scan pods")
	if err := nh.r.deleteScanPods(nh.scan, nh.nodes, nh.l); err != nil {
		nh.l.Error(err, "Cannot delete scan pods")
		return err
	}
	return nil
}

type platformScanTypeHandler struct {
	r         *ReconcileComplianceScan
	scan      *compv1alpha1.ComplianceScan
	l         logr.Logger
	platforms []corev1.Node
}

// newNodeScanTypeHandler creates a new instance of a scanTypeHandler.
// Note that it assumes that the scan instance that's given is already a copy.
func newPlatformScanTypeHandler(r *ReconcileComplianceScan, scan *compv1alpha1.ComplianceScan, logger logr.Logger) (scanTypeHandler, error) {
	return &platformScanTypeHandler{
		r:    r,
		scan: scan,
		l:    logger,
	}, nil
}

func (ph *platformScanTypeHandler) getScan() *compv1alpha1.ComplianceScan {
	return ph.scan
}

func (ph *platformScanTypeHandler) validate() (bool, error) {
	return true, nil
}

func (ph *platformScanTypeHandler) createScanWorkload() error {
	ph.l.Info("Creating a Platform scan pod")
	pod := ph.r.newPlatformScanPod(ph.scan, ph.l)
	return ph.r.launchScanPod(ph.scan, pod, ph.l)
}

func (ph *platformScanTypeHandler) handleRunningScan() (bool, error) {
	running, err := isPlatformScanPodRunning(ph.r, ph.scan, ph.l)
	if errors.IsNotFound(err) {
		// Let's go back to the previous state and make sure all the nodes are covered.
		ph.l.Info("Phase: Running: The platform scan pod is missing. Going to state LAUNCHING to make sure we launch it",
			"compliancescan")
		ph.scan.Status.Phase = compv1alpha1.PhaseLaunching
		err = ph.r.client.Status().Update(context.TODO(), ph.scan)
		if err != nil {
			return true, err
		}
		return true, nil
	} else if err != nil {
		return true, err
	}
	return running, nil
}

func (ph *platformScanTypeHandler) shouldLaunchAggregator() (bool, string, error) {
	var warnings string
	foundCM, err := getPlatformScanCM(ph.r, ph.scan)

	// Could be a transient error, so we requeue if there's any
	// error here.
	if err != nil {
		return false, "", nil
	}

	warns, ok := foundCM.Data["warnings"]
	if ok {
		warnings = warns
	}

	// NOTE: err is only set if there is an error in the scan run
	err = checkScanUnknownError(foundCM)
	if err != nil {
		return true, warnings, err
	}
	return true, warnings, nil
}

func (ph *platformScanTypeHandler) gatherResults() (compv1alpha1.ComplianceScanStatusResult, bool, error) {
	var result compv1alpha1.ComplianceScanStatusResult
	isReady := true

	foundCM, err := getPlatformScanCM(ph.r, ph.scan)

	// Could be a transient error, so we requeue if there's any
	// error here. Note that we don't persist the error.
	if err != nil {
		ph.l.Info("Platform scan has no result ConfigMap yet", "ComplianceScan.Name", ph.scan.Name)
		isReady = false
		return result, isReady, nil
	}

	cmHasResult := scanResultReady(foundCM)
	if cmHasResult == false {
		ph.l.Info("Scan results not ready, retrying. If the issue persists, restart or recreate the scan", "ComplianceScan.Name", ph.scan.Name)
		isReady = false
		return result, isReady, err
	}

	// NOTE: err is only set if there is an error in the scan run
	result, err = getScanResult(foundCM)

	// we output the last result if it was an error
	if result == compv1alpha1.ResultError {
		ph.l.Info("Platform scan error", "errMsg", err)
	}
	return result, isReady, err
}

func (ph *platformScanTypeHandler) cleanup() error {
	ph.l.Info("Deleting platform scan pods")
	if err := ph.r.deletePlatformScanPod(ph.scan, ph.l); err != nil {
		ph.l.Error(err, "Cannot delete platform scan pod")
		return err
	}
	return nil
}
