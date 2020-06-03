package utils

import "os"

type ComplianceComponent uint

const (
	OPENSCAP = iota
	OPERATOR
)

var componentDefaults = []struct {
	defaultImage string
	envVar       string
}{
	{"quay.io/jhrozek/openscap-ocp:latest", "OPENSCAP_IMAGE"},
	{"quay.io/compliance-operator/compliance-operator:latest", "OPERATOR_IMAGE"},
}

// GetComponentImage returns a full image pull spec for a given component
// based on the component type
func GetComponentImage(component ComplianceComponent) string {
	comp := componentDefaults[component]

	imageTag := os.Getenv(comp.envVar)
	if imageTag == "" {
		imageTag = comp.defaultImage
	}
	return imageTag
}
