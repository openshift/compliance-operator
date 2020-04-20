package utils

import (
	"fmt"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

// ParseMachineConfig parses a Machineconfig object from an unstructured object
// for a specific remediation.
func ParseMachineConfig(rem *compv1alpha1.ComplianceRemediation, obj *unstructured.Unstructured) (*mcfgv1.MachineConfig, error) {
	mcfg := &mcfgv1.MachineConfig{}
	unstruct := obj.UnstructuredContent()
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstruct, mcfg)

	if err != nil {
		return nil, fmt.Errorf("The MachineConfig in the remediation '%s' is not valid: %s", rem.Name, err)
	}
	return mcfg, nil
}
