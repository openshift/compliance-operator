package compliancesuite

import (
	"context"

	"github.com/go-logr/logr"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/robfig/cron"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/openshift/compliance-operator/pkg/utils"
)

const rerunnerServiceAccount = "rerunner"

func (r *ReconcileComplianceSuite) reconcileScanRerunnerCronJob(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	rerunner := getRerunner(suite)
	if suite.Spec.Schedule == "" {
		return r.handleDelete(rerunner, logger)
	}
	return r.handleCreate(suite, rerunner, logger)
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

func (r *ReconcileComplianceSuite) handleCreate(suite *compv1alpha1.ComplianceSuite, rerunner *batchv1beta1.CronJob, logger logr.Logger) error {
	key := types.NamespacedName{Name: rerunner.GetName(), Namespace: rerunner.GetNamespace()}
	found := &batchv1beta1.CronJob{}
	err := r.client.Get(context.TODO(), key, found)
	if err != nil && errors.IsNotFound(err) {
		// No re-runner found, create it
		logger.Info("Creating rerunner", "CronJob.Name", rerunner.GetName())
		return r.client.Create(context.TODO(), rerunner)
	} else if err != nil {
		return err
	}
	if found.Spec.Schedule != suite.Spec.Schedule {
		cronJobCopy := found.DeepCopy()
		cronJobCopy.Spec.Schedule = suite.Spec.Schedule
		logger.Info("Updating rerunner", "CronJob.Name", rerunner.GetName())
		return r.client.Update(context.TODO(), cronJobCopy)
	}
	return nil
}

func (r *ReconcileComplianceSuite) handleDelete(rerunner *batchv1beta1.CronJob, logger logr.Logger) error {
	key := types.NamespacedName{Name: rerunner.GetName(), Namespace: rerunner.GetNamespace()}
	found := &batchv1beta1.CronJob{}
	err := r.client.Get(context.TODO(), key, found)
	if err != nil && errors.IsNotFound(err) {
		// No re-runner found, we're good
		return nil
	} else if err != nil {
		return err
	}
	logger.Info("Deleting rerunner", "CronJob.Name", rerunner.GetName())
	return r.client.Delete(context.TODO(), rerunner)
}

// GetRerunnerName gets the name of the rerunner workload based on the suite name
func GetRerunnerName(suiteName string) string {
	return suiteName + "-rerunner"
}

func getRerunner(suite *compv1alpha1.ComplianceSuite) *batchv1beta1.CronJob {
	return &batchv1beta1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetRerunnerName(suite.Name),
			Namespace: common.GetComplianceOperatorNamespace(),
		},
		Spec: batchv1beta1.CronJobSpec{
			Schedule: suite.Spec.Schedule,
			JobTemplate: batchv1beta1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							ServiceAccountName: rerunnerServiceAccount,
							RestartPolicy:      corev1.RestartPolicyOnFailure,
							Containers: []corev1.Container{
								{
									Name:  "rerunner",
									Image: utils.GetComponentImage(utils.OPERATOR),
									Command: []string{
										"compliance-operator", "suitererunner",
										"--name", suite.GetName(),
										"--namespace", suite.GetNamespace(),
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("10m"),
											corev1.ResourceMemory: resource.MustParse("20Mi"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
