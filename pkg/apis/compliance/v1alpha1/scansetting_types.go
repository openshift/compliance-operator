package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AllRoles = "@all"
)

// +kubebuilder:object:root=true

// ScanSetting is the Schema for the scansettings API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=scansettings,scope=Namespaced,shortName=ss
type ScanSetting struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	ComplianceSuiteSettings `json:",inline"`
	ComplianceScanSettings  `json:",inline"`
	// The list of roles to apply node-specific checks to.
	//
	// This will be translated to the standard Kubernetes
	// role label `node-role.kubernetes.io/<role name>`.
	//
	// It's also possible to specify `@all` as a role, which
	// will run a scan on all nodes by not specifying a node
	// selector as we normally do. The usage of `@all` in
	// OpenShift is discouraged as the operator won't
	// be able to apply remediations unless roles are specified.
	//
	// Note that tolerations must still be configured for
	// the opeartor to appropriately schedule scans.
	Roles []string `json:"roles,omitempty"`
}

// +kubebuilder:object:root=true

// ScanSettingList contains a list of ScanSetting
type ScanSettingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScanSetting `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ScanSetting{}, &ScanSettingList{})
}
