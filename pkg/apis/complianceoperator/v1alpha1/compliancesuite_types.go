package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ComplianceScanSpecWrapper provides a ComplianceScanSpec and a Name
// +k8s:openapi-gen=true
type ComplianceScanSpecWrapper struct {
	ComplianceScanSpec `json:",inline"`

	Name string `json:"name,omitempty"`
}

// ComplianceScanStatusWrapper provides a ComplianceScanStatus and a Name
// +k8s:openapi-gen=true
type ComplianceScanStatusWrapper struct {
	ComplianceScanStatus `json:",inline"`

	Name string `json:"name,omitempty"`
}

// +k8s:openapi-gen=true
type ComplianceRemediationNameStatus struct {
	ComplianceRemediationSpecMeta `json:",inline"`
	RemediationName               string `json:"remediationName"`
	ScanName                      string `json:"scanName"`
}

// ComplianceSuiteSpec defines the desired state of ComplianceSuite
// +k8s:openapi-gen=true
type ComplianceSuiteSpec struct {
	// Should remediations be applied automatically?
	AutoApplyRemediations bool `json:"autoApplyRemediations,omitempty"`
	// +listType=atomic
	Scans []ComplianceScanSpecWrapper `json:"scans"`
}

// GetScanNameFromSuite Gets us a predictable name for the underlying
// ComplianceScans that the Suite deploys
func GetScanNameFromSuite(suite *ComplianceSuite, scanWrapName string) string {
	return suite.Name + "-" + scanWrapName
}

// ComplianceSuiteStatus defines the observed state of ComplianceSuite
// +k8s:openapi-gen=true
type ComplianceSuiteStatus struct {
	// +listType=atomic
	ScanStatuses []ComplianceScanStatusWrapper `json:"scanStatuses"`
	// +listType=atomic
	// +optional
	RemediationOverview []ComplianceRemediationNameStatus `json:"remediationOverview,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ComplianceSuite is the Schema for the compliancesuites API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=compliancesuites,scope=Namespaced
type ComplianceSuite struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComplianceSuiteSpec   `json:"spec,omitempty"`
	Status ComplianceSuiteStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ComplianceSuiteList contains a list of ComplianceSuite
type ComplianceSuiteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComplianceSuite `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ComplianceSuite{}, &ComplianceSuiteList{})
}
