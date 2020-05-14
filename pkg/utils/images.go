package utils

import "os"

type ComplianceComponent uint

const (
	LOG_COLLECTOR = iota
	OPENSCAP
	RESULT_SERVER
	AGGREGATOR
	API_RESOURCE_COLLECTOR
	OPERATOR
)

var componentDefaults = []struct {
	defaultImage string
	envVar       string
}{
	{"quay.io/compliance-operator/resultscollector:latest", "LOG_COLLECTOR_IMAGE"},
	{"quay.io/jhrozek/openscap-ocp:latest", "OPENSCAP_IMAGE"},
	{"quay.io/compliance-operator/resultserver:latest", "RESULT_SERVER_IMAGE"},
	{"quay.io/compliance-operator/remediation-aggregator", "REMEDIATION_AGGREGATOR_IMAGE"},
	{"quay.io/compliance-operator/api-resource-collector:latest", "API_RESOURCE_COLLECTOR_IMAGE"},
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
