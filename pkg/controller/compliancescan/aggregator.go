package compliancescan

import (
	"context"
	"fmt"
	"path"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
)

func createAggregatorPodName(scanName string) string {
	return dnsLengthName("aggregator-pod-", "aggregator-pod-%s", scanName)
}

func newAggregatorPod(scanInstance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) *corev1.Pod {
	podName := createAggregatorPodName(scanInstance.Name)

	podLabels := map[string]string{
		"complianceScan": scanInstance.Name,
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: scanInstance.Namespace,
			Labels:    podLabels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "compliance-operator",
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
					Image: GetComponentImage(AGGREGATOR),
					Args: []string{
						"--content=" + absContentPath(scanInstance.Spec.Content),
						"--scan=" + scanInstance.Name,
						"--namespace=" + scanInstance.Namespace,
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueVal,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "content-dir",
							MountPath: "/content",
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

func (r *ReconcileComplianceScan) launchAggregatorPod(scanInstance *complianceoperatorv1alpha1.ComplianceScan, pod *corev1.Pod, logger logr.Logger) error {
	// Make use of optimistic concurrency and just try creating the pod
	err := r.client.Create(context.TODO(), pod)
	if err != nil && !errors.IsAlreadyExists(err) {
		logger.Error(err, "Cannot launch pod", "pod", pod)
		return err
	}

	if errors.IsAlreadyExists(err) {
		// If the pod was already created, just return
		return nil
	}

	if scanInstance.Annotations == nil {
		scanInstance.Annotations = make(map[string]string)
	}

	scanInstance.Annotations[AggregatorPodAnnotation] = pod.Name
	return r.client.Update(context.TODO(), scanInstance)
}

func (r *ReconcileComplianceScan) deleteAggregator(instance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) error {
	aggregator := newAggregatorPod(instance, logger)
	err := r.client.Delete(context.TODO(), aggregator)
	if err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Cannot delete aggregator pod", "pod", aggregator)
		return err
	}

	return nil
}

func isAggregatorRunning(r *ReconcileComplianceScan, scanInstance *complianceoperatorv1alpha1.ComplianceScan, logger logr.Logger) (bool, error) {
	logger.Info("Checking aggregator pod for scan", "scan", scanInstance.Name)

	podName := scanInstance.Annotations[AggregatorPodAnnotation]
	return isPodRunning(r, podName, scanInstance.Namespace, logger)
}
