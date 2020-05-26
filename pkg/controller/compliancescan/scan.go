package compliancescan

import (
	"context"
	"fmt"
	"path"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
)

const (
	resultscollectorSA     = "resultscollector"
	apiResourceCollectorSA = "api-resource-collector"
	tailoringCMVolumeName  = "tailoring"
)

func (r *ReconcileComplianceScan) createScanPods(instance *compv1alpha1.ComplianceScan, nodes corev1.NodeList, logger logr.Logger) error {
	switch t := instance.Spec.ScanType; t {
	case compv1alpha1.ScanTypePlatform:
		return r.createPlatformScanPod(instance, logger)
	default: // ScanTypeNode
		return r.createNodeScanPods(instance, nodes, logger)
	}
}

func (r *ReconcileComplianceScan) createNodeScanPods(instance *compv1alpha1.ComplianceScan, nodes corev1.NodeList, logger logr.Logger) error {
	// On each eligible node..
	for _, node := range nodes.Items {
		// ..schedule a pod..
		logger.Info("Creating a pod for node", "Pod.Name", node.Name)
		pod := newScanPodForNode(instance, &node, logger)

		if instance.Spec.TailoringConfigMap != nil {
			if err := r.reconcileTailoring(instance, pod, logger); err != nil {
				return err
			}
		}

		// ..and launch it..
		err := r.client.Create(context.TODO(), pod)
		if errors.IsAlreadyExists(err) {
			logger.Info("Pod already exists. This is fine.", "Pod.Name", pod)
		} else if err != nil {
			log.Error(err, "Failed to launch a pod", "Pod.Name", pod)
			return err
		} else {
			logger.Info("Launched a pod", "Pod.Name", pod)
		}
	}

	return nil
}

func (r *ReconcileComplianceScan) createPlatformScanPod(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("Creating a Platform scan pod")
	pod := newPlatformScanPod(instance, logger)

	err := r.client.Create(context.TODO(), pod)
	if errors.IsAlreadyExists(err) {
		logger.Info("Pod already exists. This is fine.", "pod", pod)
	} else if err != nil {
		log.Error(err, "Failed to launch a pod", "pod", pod)
		return err
	} else {
		logger.Info("Launched a pod", "pod", pod)
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
			Namespace: common.GetComplianceOperatorNamespace(),
			Labels:    podLabels,
			Annotations: map[string]string{
				"openshift.io/scc": "privileged",
			},
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
					Image: utils.GetComponentImage(utils.LOG_COLLECTOR),
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
					Image:   utils.GetComponentImage(utils.OPENSCAP),
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
				corev1.LabelHostname: node.Labels[corev1.LabelHostname],
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

func newPlatformScanPod(scanInstance *compv1alpha1.ComplianceScan, logger logr.Logger) *corev1.Pod {
	mode := int32(0744)
	podName := getPodForNodeName(scanInstance.Name, PlatformScanName)
	cmName := getConfigMapForNodeName(scanInstance.Name, PlatformScanName)
	podLabels := map[string]string{
		"complianceScan": scanInstance.Name,
	}
	collectorArgs := []string{
		"--content=/content/" + scanInstance.Spec.Content,
		"--resultdir=" + PlatformScanDataRoot,
		"--profile=" + scanInstance.Spec.Profile,
	}

	if scanInstance.Spec.Debug {
		collectorArgs = append(collectorArgs, "--debug")
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: common.GetComplianceOperatorNamespace(),
			Labels:    podLabels,
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: apiResourceCollectorSA,
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
				{
					Name:            PlatformScanResourceCollectorName,
					Image:           utils.GetComponentImage(utils.API_RESOURCE_COLLECTOR),
					Args:            collectorArgs,
					ImagePullPolicy: corev1.PullAlways,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "content-dir",
							MountPath: "/content",
						},
						{
							Name:      "fetch-results",
							MountPath: PlatformScanDataRoot,
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "log-collector",
					Image: utils.GetComponentImage(utils.LOG_COLLECTOR),
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
					Image:   utils.GetComponentImage(utils.OPENSCAP),
					Command: []string{OpenScapScriptPath},
					VolumeMounts: []corev1.VolumeMount{
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
							Name:      "fetch-results",
							MountPath: PlatformScanDataRoot,
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
									Name: envCmForPlatformScan(scanInstance),
								},
							},
						},
					},
				},
			},
			NodeSelector: map[string]string{
				"node-role.kubernetes.io/master": "",
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      "node-role.kubernetes.io/master",
					Operator: "Exists",
					Effect:   "NoSchedule",
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
			Volumes: []corev1.Volume{
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
					Name: "fetch-results",
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
		logger.Info("Deleting a pod on node", "node", node.Name)
		pod := newScanPodForNode(instance, &node, logger)

		// Delete it.
		err := r.client.Delete(context.TODO(), pod)
		if errors.IsNotFound(err) {
			logger.Info("Pod is already gone. This is fine.", "Pod.Name", pod)
		} else if err != nil {
			log.Error(err, "Failed to delete a pod", "Pod.Name", pod)
			return err
		} else {
			logger.Info("deleted pod", "Pod.Name", pod)
		}
	}

	return nil
}

func (r *ReconcileComplianceScan) reconcileTailoring(instance *compv1alpha1.ComplianceScan, pod *corev1.Pod, logger logr.Logger) error {
	if instance.Spec.TailoringConfigMap.Name == "" {
		return common.NewNonRetriableCtrlError("tailoring config map name can't be empty")
	}
	name := instance.Spec.TailoringConfigMap.Name
	ns := instance.Namespace

	tailoringCMName := getReplicatedTailoringCMName(instance.Name)
	tailoringCMNamespace := common.GetComplianceOperatorNamespace()
	if err := r.reconcileReplicatedTailoringConfigMap(name, ns, tailoringCMName, tailoringCMNamespace, instance.Name, logger); err != nil {
		return err
	}

	if err := r.addTailoringVolume(tailoringCMName, pod); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileComplianceScan) addTailoringVolume(name string, pod *corev1.Pod) error {
	mode := int32(0644)

	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: tailoringCMVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
				DefaultMode: &mode,
			},
		},
	})

	// The index is used to get the references instead of copies
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		if container.Name == OpenSCAPScanContainerName {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      tailoringCMVolumeName,
				MountPath: OpenScapTailoringDir,
				ReadOnly:  true,
			})
		}
	}

	return nil
}

func (r *ReconcileComplianceScan) deletePlatformScanPod(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("Deleting the platform scan pod for instance", "instance", instance.Name)
	pod := newPlatformScanPod(instance, logger)

	err := r.client.Delete(context.TODO(), pod)
	if errors.IsNotFound(err) {
		logger.Info("Pod is already gone. This is fine.", "pod", pod)
	} else if err != nil {
		log.Error(err, "Failed to delete a pod", "pod", pod)
		return err
	} else {
		logger.Info("deleted pod", "pod", pod)
	}

	return nil
}

// Creates a private configmap that'll only be used by this operator.
func (r *ReconcileComplianceScan) reconcileReplicatedTailoringConfigMap(origName, origNs, privName, privNs, scanName string, logger logr.Logger) error {
	logger.Info("Reconciling Tailoring ConfigMap", "ConfigMap.Name", origName, "ConfigMap.Namespace", origNs)

	origCM := &corev1.ConfigMap{}
	origKey := types.NamespacedName{Name: origName, Namespace: origNs}
	err := r.client.Get(context.TODO(), origKey, origCM)
	if err != nil && errors.IsNotFound(err) {
		return common.NewNonRetriableCtrlError("Tailoring ConfigMap not found")
	} else if err != nil {
		log.Error(err, "Failed to get spec tailoring ConfigMap", "ConfigMap.Name", origName, "ConfigMap.Namespace", origNs)
		return err
	}

	origData, ok := origCM.Data["tailoring.xml"]
	if !ok {
		return common.NewNonRetriableCtrlError("Tailoring ConfigMap missing `tailoring.xml` key")
	}
	if origData == "" {
		return common.NewNonRetriableCtrlError("Tailoring ConfigMap's key `tailoring.xml` is empty")
	}

	privCM := &corev1.ConfigMap{}
	privKey := types.NamespacedName{Name: privName, Namespace: privNs}
	err = r.client.Get(context.TODO(), privKey, privCM)
	if err != nil && errors.IsNotFound(err) {
		newCM := &corev1.ConfigMap{}
		newCM.SetName(privName)
		newCM.SetNamespace(privNs)
		if newCM.Labels == nil {
			newCM.Labels = make(map[string]string)
		}
		newCM.Labels[compv1alpha1.ScanLabel] = scanName
		newCM.Labels[compv1alpha1.ScriptLabel] = ""
		if newCM.Data == nil {
			newCM.Data = make(map[string]string)
		}
		newCM.Data["tailoring.xml"] = origData
		logger.Info("Creating private Tailoring ConfigMap", "ConfigMap.Name", privName, "ConfigMap.Namespace", privNs)
		err = r.client.Create(context.TODO(), newCM)
		// Ignore error if CM already exists
		if err != nil && !errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	} else if err != nil {
		log.Error(err, "Failed to get private tailoring ConfigMap", "ConfigMap.Name", privName, "ConfigMap.Namespace", privNs)
		return err
	}
	privData, _ := privCM.Data["tailoring.xml"]

	// privCM needs update
	if privData != origData {
		updatedCM := privCM.DeepCopy()
		if updatedCM.Data == nil {
			updatedCM.Data = make(map[string]string)
		}
		if updatedCM.Labels == nil {
			updatedCM.Labels = make(map[string]string)
		}
		updatedCM.Labels[compv1alpha1.ScanLabel] = scanName
		updatedCM.Labels[compv1alpha1.ScriptLabel] = ""
		updatedCM.Data["tailoring.xml"] = origData
		logger.Info("Updating private Tailoring ConfigMap", "ConfigMap.Name", privName, "ConfigMap.Namespace", privNs)
		return r.client.Update(context.TODO(), updatedCM)
	}
	logger.Info("Private Tailoring ConfigMap is up-to-date", "ConfigMap.Name", privName, "ConfigMap.Namespace", privNs)
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

func getReplicatedTailoringCMName(instanceName string) string {
	return utils.DNSLengthName("tp-", "tp-%s", instanceName)
}
