package compliancesuite

import (
	"context"
	"fmt"

	"github.com/ComplianceAsCode/compliance-operator/pkg/controller/common"
	"github.com/go-logr/logr"
	cron "github.com/robfig/cron/v3"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	compv1alpha1 "github.com/ComplianceAsCode/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/ComplianceAsCode/compliance-operator/pkg/utils"
)

const rerunnerServiceAccount = "rerunner"

func (r *ReconcileComplianceSuite) reconcileScanRerunnerCronJob(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	priorityClassName, err := r.getPriorityClassName(suite)
	if err != nil {
		logger.Error(err, "Cannot get priority class name, scan will not be run with set priority class")
	}
	// this is a validation and should warn the user
	if priorityClassExist, why := utils.ValidatePriorityClassExist(priorityClassName, r.Client); !priorityClassExist {
		log.Info(why, "Suite", suite.Name)
		r.Recorder.Eventf(suite, corev1.EventTypeWarning, "PriorityClass", why+" Suite:"+suite.Name)
	}
	if suite.Spec.Schedule == "" {
		return r.handleRerunnerDelete(suite, logger)
	}
	return r.handleCreate(suite, logger)
}

// validates that the provided schedule is correctly set. Else it returns false (not valid) and an
// error message
func (r *ReconcileComplianceSuite) validateSchedule(suite *compv1alpha1.ComplianceSuite) (bool, string) {
	if suite.Spec.Schedule == "" {
		return true, ""
	}
	// Verify that the Schedule is in a correct format
	_, err := cron.ParseStandard(suite.Spec.Schedule)
	if err != nil {
		return false, "ComplianceSuite's schedule is wrongly formatted"
	}
	return true, ""
}

func (r *ReconcileComplianceSuite) handleCreate(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	return r.cronJobCompatCreate(suite, reRunnerNamespacedName(suite.Name), logger)
}

// getPriorityClassName for rerunner from suite scan
func (r *ReconcileComplianceSuite) getPriorityClassName(suite *compv1alpha1.ComplianceSuite) (string, error) {
	// get priorityClass from suite scan
	scans := &compv1alpha1.ComplianceScanList{}
	scanSuiteSelector := make(map[string]string)
	scanSuiteSelector[compv1alpha1.SuiteLabel] = suite.Name
	listOpts := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(scanSuiteSelector),
		Namespace:     suite.Namespace,
	}
	err := r.Client.List(context.TODO(), scans, listOpts)
	if err != nil {
		return "", fmt.Errorf("Error while getting scans for ComplianceSuite '%s', err: %s\n", suite.Name, err)
	}
	if len(scans.Items) == 0 {
		return "", fmt.Errorf("No scans found for ComplianceSuite '%s'", suite.Name)
	}
	return scans.Items[0].Spec.PriorityClass, nil
}

func (r *ReconcileComplianceSuite) handleRerunnerDelete(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	key := reRunnerNamespacedName(suite.Name)
	found, err := cronJobCompatGet(r, key)
	if err != nil {
		return err
	}

	inNs := client.InNamespace(common.GetComplianceOperatorNamespace())
	withLabel := client.MatchingLabels{
		compv1alpha1.SuiteLabel:       suite.Name,
		compv1alpha1.SuiteScriptLabel: "",
	}
	err = r.Client.DeleteAllOf(context.Background(), &corev1.Pod{}, inNs, withLabel)
	if err != nil {
		return err
	}

	err = r.Client.DeleteAllOf(context.Background(), &batchv1.Job{}, inNs, withLabel)
	if err != nil {
		return err
	}

	logger.Info("Deleting rerunner", "CronJob.Name", key.Name)
	return cronJobCompatDelete(r, found)
}
