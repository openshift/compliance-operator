package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient

// ComplianceScanStatusPhase represents the status of the compliancescan run.
type ComplianceScanStatusPhase string

const (
	// PhasePending represents the scan pending to be scheduled
	PhasePending ComplianceScanStatusPhase = "PENDING"
	// PhaseLaunching represents being scheduled and launching pods to run the scans
	PhaseLaunching ComplianceScanStatusPhase = "LAUNCHING"
	// PhaseRunning represents the scan being ran by the pods and waiting for the results
	PhaseRunning ComplianceScanStatusPhase = "RUNNING"
	// PhaseDone represents the scan pods being done and the results being available
	PhaseDone ComplianceScanStatusPhase = "DONE"
)

// ComplianceScanSpec defines the desired state of ComplianceScan
// +k8s:openapi-gen=true
type ComplianceScanSpec struct {
	ContentImage string            `json:"contentImage,omitempty"`
	Profile      string            `json:"profile,omitempty"`
	Rule         string            `json:"rule,omitempty"`
	Content      string            `json:"content,omitempty"`
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// ComplianceScanStatus defines the observed state of ComplianceScan
// +k8s:openapi-gen=true
type ComplianceScanStatus struct {
	Phase ComplianceScanStatusPhase `json:"phase,omitempty"`
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
