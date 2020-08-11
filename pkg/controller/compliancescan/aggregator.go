package compliancescan

import (
	"context"
	"fmt"
	"path"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
)

const aggregatorSA = "remediation-aggregator"

func getAggregatorPodName(scanName string) string {
	return utils.DNSLengthName("aggregator-pod-", "aggregator-pod-%s", scanName)
}

func newAggregatorPod(scanInstance *compv1alpha1.ComplianceScan, logger logr.Logger) *corev1.Pod {
	podName := getAggregatorPodName(scanInstance.Name)

	podLabels := map[string]string{
		"complianceScan": scanInstance.Name,
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: common.GetComplianceOperatorNamespace(),
			Labels:    podLabels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: aggregatorSA,
			InitContainers: []corev1.Container{
				{
					Name:  "content-container",
					Image: getInitContainerImage(&scanInstance.Spec, logger),
					Command: []string{
						"sh",
						"-c",
						fmt.Sprintf("cp %s /content | /bin/true", path.Join("/", scanInstance.Spec.Content)),
					},
					ImagePullPolicy: corev1.PullAlways,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "content-dir",
							MountPath: "/content",
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "log-collector",
					Image: utils.GetComponentImage(utils.OPERATOR),
					Command: []string{
						"compliance-operator", "aggregator",
						"--content=" + absContentPath(scanInstance.Spec.Content),
						"--scan=" + scanInstance.Name,
						"--namespace=" + scanInstance.Namespace,
					},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("200Mi"),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "content-dir",
							MountPath: "/content",
							ReadOnly:  true,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
			Volumes: []corev1.Volume{
				{
					Name: "content-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}
}

func (r *ReconcileComplianceScan) launchAggregatorPod(scanInstance *compv1alpha1.ComplianceScan, pod *corev1.Pod, logger logr.Logger) error {
	// Make use of optimistic concurrency and just try creating the pod
	err := r.client.Create(context.TODO(), pod)
	if err != nil && !errors.IsAlreadyExists(err) {
		logger.Error(err, "Cannot launch pod", "pod", pod)
		return err
	}

	// If the pod was already created, just return
	return nil
}

func (r *ReconcileComplianceScan) deleteAggregator(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	aggregator := newAggregatorPod(instance, logger)
	err := r.client.Delete(context.TODO(), aggregator)
	if err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Cannot delete aggregator pod", "pod", aggregator)
		return err
	}

	return nil
}

func isAggregatorRunning(r *ReconcileComplianceScan, scanInstance *compv1alpha1.ComplianceScan, logger logr.Logger) (bool, error) {
	logger.Info("Checking aggregator pod for scan", "ComplianceScan.Name", scanInstance.Name)

	podName := getAggregatorPodName(scanInstance.Name)
	return isPodRunning(r, podName, common.GetComplianceOperatorNamespace(), logger)
}
