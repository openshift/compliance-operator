package utils

import (
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// IsMachineConfig checks if the specified object is a MachineConfig object
func IsMachineConfig(obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}
	// FIXME(jaosorior): Find a more dynamic way to get
	// the MachineConfig's GVK
	objgvk := obj.GroupVersionKind()
	return "MachineConfig" == objgvk.Kind && mcfgapi.GroupName == objgvk.Group
}
