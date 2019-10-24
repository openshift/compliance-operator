package compliancescan

import (
	"os"
)

type ComplianceComponent uint

const (
	LOG_COLLECTOR = iota
	OPENSCAP
)

var componentDefaults = []struct {
	defaultImage string
	envVar       string
}{
	{"quay.io/jhrozek/scapresults-k8s:latest", "LOG_COLLECTOR_IMAGE"},
	{"quay.io/jhrozek/openscap-ocp:latest", "OPENSCAP_IMAGE"},
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
