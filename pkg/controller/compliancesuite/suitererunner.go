package compliancesuite

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	cron "github.com/robfig/cron/v3"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/utils"
)

const rerunnerServiceAccount = "rerunner"

func (r *ReconcileComplianceSuite) reconcileScanRerunnerCronJob(suite *compv1alpha1.ComplianceSuite, logger logr.Logger) error {
	rerunner := getRerunner(suite)
	if suite.Spec.Schedule == "" {
		return r.handleRerunnerDelete(rerunner, suite.Name, logger)
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

func (r *ReconcileComplianceSuite) handleRerunnerDelete(rerunner *batchv1beta1.CronJob, suiteName string, logger logr.Logger) error {
	key := types.NamespacedName{Name: rerunner.GetName(), Namespace: rerunner.GetNamespace()}
	found := &batchv1beta1.CronJob{}
	err := r.client.Get(context.TODO(), key, found)
	if err != nil && errors.IsNotFound(err) {
		// No re-runner found, we're good
		return nil
	} else if err != nil {
		return err
	}

	inNs := client.InNamespace(common.GetComplianceOperatorNamespace())
	withLabel := client.MatchingLabels{
		compv1alpha1.SuiteLabel:       suiteName,
		compv1alpha1.SuiteScriptLabel: "",
	}
	err = r.client.DeleteAllOf(context.Background(), &corev1.Pod{}, inNs, withLabel)
	if err != nil {
		return err
	}

	err = r.client.DeleteAllOf(context.Background(), &batchv1.Job{}, inNs, withLabel)
	if err != nil {
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
	falseP := false
	trueP := true
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
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								compv1alpha1.SuiteLabel:       suite.Name,
								compv1alpha1.SuiteScriptLabel: "",
								"workload":                    "suitererunner",
							},
							Annotations: map[string]string{
								"workload.openshift.io/management": `{"effect": "PreferredDuringScheduling"}`,
							},
						},
						Spec: corev1.PodSpec{
							NodeSelector: map[string]string{
								"node-role.kubernetes.io/master": "",
							},
							Tolerations: []corev1.Toleration{
								{
									Key:      "node-role.kubernetes.io/master",
									Operator: corev1.TolerationOpExists,
									Effect:   corev1.TaintEffectNoSchedule,
								},
							},
							ServiceAccountName: rerunnerServiceAccount,
							RestartPolicy:      corev1.RestartPolicyOnFailure,
							Containers: []corev1.Container{
								{
									Name:  "rerunner",
									Image: utils.GetComponentImage(utils.OPERATOR),
									SecurityContext: &corev1.SecurityContext{
										AllowPrivilegeEscalation: &falseP,
										ReadOnlyRootFilesystem:   &trueP,
									},
									Command: []string{
										"compliance-operator", "suitererunner",
										"--name", suite.GetName(),
										"--namespace", suite.GetNamespace(),
									},
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceMemory: resource.MustParse("10Mi"),
											corev1.ResourceCPU:    resource.MustParse("10m"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceMemory: resource.MustParse("50Mi"),
											corev1.ResourceCPU:    resource.MustParse("50m"),
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
