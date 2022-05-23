package utils

import (
	"context"

	compv1alpha1 "github.com/ComplianceAsCode/compliance-operator/pkg/apis/compliance/v1alpha1"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func IsKind(obj *unstructured.Unstructured, kind string) bool {
	if obj == nil {
		return false
	}
	// FIXME(jaosorior): Find a more dynamic way to get
	// the MachineConfig's GVK
	objgvk := obj.GroupVersionKind()
	return kind == objgvk.Kind && mcfgapi.GroupName == objgvk.Group
}

// IsMachineConfig checks if the specified object is a MachineConfig object
func IsMachineConfig(obj *unstructured.Unstructured) bool {
	return IsKind(obj, "MachineConfig")
}

func IsKubeletConfig(obj *unstructured.Unstructured) bool {
	return IsKind(obj, "KubeletConfig")
}

func HaveOutdatedRemediations(client runtimeclient.Client) (error, bool) {
	remList := &compv1alpha1.ComplianceRemediationList{}
	listOpts := runtimeclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{compv1alpha1.OutdatedRemediationLabel: ""}),
	}

	if err := client.List(context.TODO(), remList, &listOpts); err != nil {
		return err, false
	}

	return nil, len(remList.Items) > 0
}
