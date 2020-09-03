package utils

import (
	corev1 "k8s.io/api/core/v1"
)

// FindNewestPod finds the newest pod in the given set
func FindNewestPod(pods []corev1.Pod) *corev1.Pod {
	var newestPod *corev1.Pod
	for _, pod := range pods {
		if newestPod == nil {
			newestPod = pod.DeepCopy()
		} else if newestPod.CreationTimestamp.Before(&pod.CreationTimestamp) {
			newestPod = pod.DeepCopy()
		}
	}
	return newestPod
}
