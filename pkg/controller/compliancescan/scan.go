package compliancescan

import (
	"context"
	"fmt"
	"path"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

const resultscollectorSA = "resultscollector"

func (r *ReconcileComplianceScan) createScanPods(instance *compv1alpha1.ComplianceScan, nodes corev1.NodeList, logger logr.Logger) error {
	// On each eligible node..
	for _, node := range nodes.Items {
		// ..schedule a pod..
		logger.Info("Creating a pod for node", "node", node.Name)
		pod := newScanPodForNode(instance, &node, logger)
		if err := controllerutil.SetControllerReference(instance, pod, r.scheme); err != nil {
			log.Error(err, "Failed to set pod ownership", "pod", pod)
			return err
		}

		// ..and launch it..
		err := r.client.Create(context.TODO(), pod)
		if errors.IsAlreadyExists(err) {
			logger.Info("Pod already exists. This is fine.", "pod", pod)
		} else if err != nil {
			log.Error(err, "Failed to launch a pod", "pod", pod)
			return err
		} else {
			logger.Info("Launched a pod", "pod", pod)
		}
	}

	// make sure the instance is updated with the node-pod labels
	if err := r.client.Update(context.TODO(), instance); err != nil {
		return err
	}
	return nil
}

func newScanPodForNode(scanInstance *compv1alpha1.ComplianceScan, node *corev1.Node, logger logr.Logger) *corev1.Pod {

	mode := int32(0744)

	podName := getPodForNodeName(scanInstance.Name, node.Name)
	cmName := getConfigMapForNodeName(scanInstance.Name, node.Name)
	podLabels := map[string]string{
		"complianceScan": scanInstance.Name,
		"targetNode":     node.Name,
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: scanInstance.Namespace,
			Labels:    podLabels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: resultscollectorSA,
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
					Image: GetComponentImage(LOG_COLLECTOR),
					Args: []string{
						"--arf-file=/reports/report-arf.xml",
						"--results-file=/reports/report.xml",
						"--exit-code-file=/reports/exit_code",
						"--oscap-output-file=/reports/cmd_output",
						"--config-map-name=" + cmName,
						"--owner=" + scanInstance.Name,
						"--namespace=" + scanInstance.Namespace,
						"--resultserveruri=" + getResultServerURI(scanInstance),
						"--tls-client-cert=/etc/pki/tls/tls.crt",
						"--tls-client-key=/etc/pki/tls/tls.key",
						"--tls-ca=/etc/pki/tls/ca.crt",
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueVal,
					},
					ImagePullPolicy: corev1.PullAlways,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "report-dir",
							MountPath: "/reports",
							ReadOnly:  true,
						},
						{
							Name:      "tls",
							MountPath: "/etc/pki/tls",
							ReadOnly:  true,
						},
					},
				},
				{
					Name:    OpenSCAPScanContainerName,
					Image:   GetComponentImage(OPENSCAP),
					Command: []string{OpenScapScriptPath},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueVal,
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "host",
							MountPath: "/host",
							ReadOnly:  true,
						},
						{
							Name:      "report-dir",
							MountPath: "/reports",
						},
						{
							Name:      "content-dir",
							MountPath: "/content",
							ReadOnly:  true,
						},
						{
							Name:      scriptCmForScan(scanInstance),
							MountPath: "/scripts",
							ReadOnly:  true,
						},
					},
					EnvFrom: []corev1.EnvFromSource{
						{
							ConfigMapRef: &corev1.ConfigMapEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: envCmForScan(scanInstance),
								},
							},
						},
					},
				},
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      "node-role.kubernetes.io/master",
					Operator: "Exists",
					Effect:   "NoSchedule",
				},
			},
			NodeSelector: map[string]string{
				NodeHostnameLabel: node.Labels[NodeHostnameLabel],
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
			Volumes: []corev1.Volume{
				{
					Name: "host",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/",
							Type: &hostPathDir,
						},
					},
				},
				{
					Name: "report-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "content-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: scriptCmForScan(scanInstance),
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: scriptCmForScan(scanInstance),
							},
							DefaultMode: &mode,
						},
					},
				},
				{
					Name: "tls",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: ClientCertPrefix + scanInstance.Name,
						},
					},
				},
			},
		},
	}
}

func (r *ReconcileComplianceScan) deleteScanPods(instance *compv1alpha1.ComplianceScan, nodes corev1.NodeList, logger logr.Logger) error {
	// On each eligible node..
	for _, node := range nodes.Items {
		// ..schedule a pod..
		logger.Info("Creating a pod for node", "node", node.Name)
		pod := newScanPodForNode(instance, &node, logger)

		// ..and launch it..
		err := r.client.Delete(context.TODO(), pod)
		if errors.IsNotFound(err) {
			logger.Info("Pod is already gone. This is fine.", "pod", pod)
		} else if err != nil {
			log.Error(err, "Failed to delete a pod", "pod", pod)
			return err
		} else {
			logger.Info("deleted pod", "pod", pod)
		}
	}

	return nil
}

func getScanResult(cm *corev1.ConfigMap) (compv1alpha1.ComplianceScanStatusResult, error) {
	exitcode, ok := cm.Data["exit-code"]
	if ok {
		switch exitcode {
		case "0":
			return compv1alpha1.ResultCompliant, nil
		case "2":
			return compv1alpha1.ResultNonCompliant, nil
		default:
			errorMsg, ok := cm.Data["error-msg"]
			if ok {
				return compv1alpha1.ResultError, fmt.Errorf(errorMsg)
			}
			return compv1alpha1.ResultError, fmt.Errorf("The ConfigMap '%s' was missing 'error-msg'", cm.Name)
		}
	}
	return compv1alpha1.ResultError, fmt.Errorf("The ConfigMap '%s' was missing 'exit-code'", cm.Name)
}
