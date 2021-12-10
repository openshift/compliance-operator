package utils

import (
	corev1 "k8s.io/api/core/v1"
)

type CtlplaneSchedulingInfo struct {
	Selector    map[string]string
	Tolerations []corev1.Toleration
}
