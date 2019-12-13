package v1

import (
	igntypes "github.com/coreos/ignition/config/v2_2/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MachineConfigSpec defines the desired state of MachineConfig
type MachineConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	// OSImageURL specifies the remote location that will be used to
	// fetch the OS.
	OSImageURL string `json:"osImageURL"`
	// Config is a Ignition Config object.
	Config igntypes.Config `json:"config"`

	KernelArguments []string `json:"kernelArguments"`

	FIPS bool `json:"fips"`
}

// MachineConfig is the Schema for the machineconfigs API
// +genclient
// +genclient:noStatus
// +genclient:nonNamespaced
// +k8s:deepcopy-gen=false
type MachineConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MachineConfigSpec `json:"spec,omitempty"`
}

// MachineConfigList contains a list of MachineConfig
// +k8s:deepcopy-gen=false
type MachineConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MachineConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MachineConfig{}, &MachineConfigList{})
}
