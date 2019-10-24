package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	PhasePending   = "PENDING"
	PhaseLaunching = "LAUNCHING"
	PhaseRunning   = "RUNNING"
	PhaseDone      = "DONE"
)

// ComplianceScanSpec defines the desired state of ComplianceScan
// +k8s:openapi-gen=true
type ComplianceScanSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Profile string `json:"profile,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Content string `json:"content,omitempty"`
}

// ComplianceScanStatus defines the observed state of ComplianceScan
// +k8s:openapi-gen=true
type ComplianceScanStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Phase string `json:"phase,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ComplianceScan is the Schema for the compliancescans API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
type ComplianceScan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComplianceScanSpec   `json:"spec,omitempty"`
	Status ComplianceScanStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ComplianceScanList contains a list of ComplianceScan
type ComplianceScanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComplianceScan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ComplianceScan{}, &ComplianceScanList{})
}
