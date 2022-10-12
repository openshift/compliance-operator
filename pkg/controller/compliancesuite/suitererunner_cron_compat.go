package compliancesuite

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	compv1alpha1 "github.com/ComplianceAsCode/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/ComplianceAsCode/compliance-operator/pkg/controller/common"
	"github.com/ComplianceAsCode/compliance-operator/pkg/utils"
)

// GetRerunnerName gets the name of the rerunner workload based on the suite name
func GetRerunnerName(suiteName string) string {
	// Operator SDK doesn't allow CronJob with names longer than 52
	// characters. Trim everything but the first 42 characters so we have
	// enough room for the "-rerunner" string.
	if len(suiteName) >= 42 {
		suiteName = suiteName[0:42]
	}
	return suiteName + "-rerunner"
}

func (r *ReconcileComplianceSuite) cronJobCompatCreate(
	suite *compv1alpha1.ComplianceSuite,
	key types.NamespacedName,
	logger logr.Logger,
) error {
	var getObj client.Object

	priorityClassName, err := r.getPriorityClassName(suite)
	if err != nil {
		logger.Error(err, "Cannot get priority class name, scan will not be run with set priority class")
	}

	createBeta := func() *batchv1beta1.CronJob {
		getObj = &batchv1beta1.CronJob{}
		return r.getBetaV1Rerunner(suite, priorityClassName)
	}

	createV1 := func() *batchv1.CronJob {
		getObj = &batchv1.CronJob{}
		return r.getV1Rerunner(suite, priorityClassName)
	}

	updateBeta := func() error {
		getObjTyped, ok := getObj.(*batchv1beta1.CronJob)
		if !ok {
			return fmt.Errorf("failed to cast object to beta CronJob")
		}
		if getObjTyped.Spec.Schedule == suite.Spec.Schedule {
			return nil
		}
		cronJobCopy := getObjTyped.DeepCopy()
		cronJobCopy.Spec.Schedule = suite.Spec.Schedule
		logger.Info("Updating beta rerunner", "CronJob.Name", cronJobCopy.GetName())
		return r.Client.Update(context.TODO(), cronJobCopy)
	}

	updateV1 := func() error {
		getObjTyped, ok := getObj.(*batchv1.CronJob)
		if !ok {
			return fmt.Errorf("failed to cast object to v1 CronJob")
		}
		if getObjTyped.Spec.Schedule == suite.Spec.Schedule {
			return nil
		}
		cronJobCopy := getObjTyped.DeepCopy()
		cronJobCopy.Spec.Schedule = suite.Spec.Schedule
		logger.Info("Updating v1 rerunner", "CronJob.Name", cronJobCopy.GetName())
		return r.Client.Update(context.TODO(), cronJobCopy)
	}

	createAction := func(o client.Object) error {
		err := r.Client.Get(context.TODO(), key, getObj)
		if err != nil && errors.IsNotFound(err) {
			// No re-runner found, create it
			logger.Info("Creating rerunner", "CronJob.Name", o.GetName())
			return r.Client.Create(context.TODO(), o)
		} else if err != nil {
			return err
		}

		switch o.(type) {
		case *batchv1beta1.CronJob:
			return updateBeta()
		case *batchv1.CronJob:
			return updateV1()
		}

		return nil
	}

	return doCompat(createAction, createBeta, createV1)
}

func cronJobCompatGet(r *ReconcileComplianceSuite, key types.NamespacedName) (client.Object, error) {
	var retObj client.Object

	getEmptyBeta := func() *batchv1beta1.CronJob {
		return &batchv1beta1.CronJob{}
	}

	getEmptyV1 := func() *batchv1.CronJob {
		return &batchv1.CronJob{}
	}

	getAction := func(o client.Object) error {
		err := r.Client.Get(context.TODO(), key, o)
		if err != nil && errors.IsNotFound(err) {
			// No re-runner found, we're good
			return nil
		} else if err != nil {
			return err
		}

		retObj = o
		return nil
	}

	err := doCompat(getAction, getEmptyBeta, getEmptyV1)
	return retObj, err
}

func cronJobCompatDelete(r *ReconcileComplianceSuite, cron client.Object) error {
	if cron == nil {
		// for cases where cronJobCompatGet returns nil,nil
		return nil
	}

	return r.Client.Delete(context.TODO(), cron)
}

type compatAction func(o client.Object) error
type getBetaCron func() *batchv1beta1.CronJob
type getV1Cron func() *batchv1.CronJob

func doCompat(what compatAction, betaCron getBetaCron, v1cron getV1Cron) error {
	err := what(v1cron())
	if meta.IsNoMatchError(err) {
		return what(betaCron())
	}
	return err
}

func reRunnerNamespacedName(suiteName string) types.NamespacedName {
	return types.NamespacedName{
		Name:      GetRerunnerName(suiteName),
		Namespace: common.GetComplianceOperatorNamespace(),
	}
}

func reRunnerObjectMeta(suiteName string) *metav1.ObjectMeta {
	nsName := reRunnerNamespacedName(suiteName)

	return &metav1.ObjectMeta{
		Name:      nsName.Name,
		Namespace: nsName.Namespace,
	}
}

func (r *ReconcileComplianceSuite) getV1Rerunner(
	suite *compv1alpha1.ComplianceSuite,
	priorityClassName string,
) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: *reRunnerObjectMeta(suite.Name),
		Spec: batchv1.CronJobSpec{
			Schedule: suite.Spec.Schedule,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: *r.getRerunnerPodTemplate(suite, priorityClassName),
				},
			},
		},
	}
}

func (r *ReconcileComplianceSuite) getBetaV1Rerunner(
	suite *compv1alpha1.ComplianceSuite,
	priorityClassName string,
) *batchv1beta1.CronJob {
	return &batchv1beta1.CronJob{
		ObjectMeta: *reRunnerObjectMeta(suite.Name),
		Spec: batchv1beta1.CronJobSpec{
			Schedule: suite.Spec.Schedule,
			JobTemplate: batchv1beta1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: *r.getRerunnerPodTemplate(suite, priorityClassName),
				},
			},
		},
	}
}

func (r *ReconcileComplianceSuite) getRerunnerPodTemplate(
	suite *compv1alpha1.ComplianceSuite,
	priorityClassName string,
) *corev1.PodTemplateSpec {
	falseP := false
	trueP := true

	// We need to support both v1 and beta1 CronJobs, so we need to use the
	// same pod template for both. We can't use the same CronJob object
	// because the API is different.
	return &corev1.PodTemplateSpec{
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
			NodeSelector:       r.schedulingInfo.Selector,
			Tolerations:        r.schedulingInfo.Tolerations,
			ServiceAccountName: rerunnerServiceAccount,
			RestartPolicy:      corev1.RestartPolicyOnFailure,
			PriorityClassName:  priorityClassName,
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
							corev1.ResourceMemory: resource.MustParse("20Mi"),
							corev1.ResourceCPU:    resource.MustParse("10m"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("100Mi"),
							corev1.ResourceCPU:    resource.MustParse("50m"),
						},
					},
				},
			},
		},
	}
}
