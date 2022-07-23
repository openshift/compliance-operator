package utils

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	schedulev1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// validate priority class exists by name
func ValidatePriorityClassExist(name string, client client.Client) (bool, string) {
	if name == "" {
		return true, ""
	}
	priorityClass := &schedulev1.PriorityClass{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: name}, priorityClass)
	if err != nil {
		return false, fmt.Sprintf("Error while getting priority class '%s', err: %s\n", name, err)
	}
	return true, ""
}
