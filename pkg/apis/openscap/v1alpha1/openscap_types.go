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

// OpenScapSpec defines the desired state of OpenScap
// +k8s:openapi-gen=true
type OpenScapSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Profile string `json:"profile,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Content string `json:"content,omitempty"`
}

// OpenScapStatus defines the observed state of OpenScap
// +k8s:openapi-gen=true
type OpenScapStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Phase string `json:"phase,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// OpenScap is the Schema for the openscaps API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
type OpenScap struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OpenScapSpec   `json:"spec,omitempty"`
	Status OpenScapStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// OpenScapList contains a list of OpenScap
type OpenScapList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenScap `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OpenScap{}, &OpenScapList{})
}
