package common

import (
	"io/ioutil"
	"os"
	"strings"
)

var (
	complianceOperatorNamespace = "openshift-compliance"
	complianceOperatorName      = "compliance-operator"
)

type RunModeType string

const (
	// OpenSCAPExitCodeCompliant defines a success coming from OpenSCAP
	OpenSCAPExitCodeCompliant string = "0"
	// OpenSCAPExitCodeNonCompliant defines a non-compliance error coming from OpenSCAP
	OpenSCAPExitCodeNonCompliant string = "2"
	// PodUnschedulableExitCode is a custom error that indicates that we couldn't schedule the pod
	PodUnschedulableExitCode string = "unschedulable"

	// taken from k8sutil
	ForceRunModeEnv             = "OSDK_FORCE_RUN_MODE"
	LocalRunMode    RunModeType = "local"
	ClusterRunMode  RunModeType = "cluster"
)

func init() {
	name, ok := os.LookupEnv("OPERATOR_NAME")
	if ok {
		complianceOperatorName = name
	}

	if isRunModeLocal() {
		ns, ok := os.LookupEnv("OPERATOR_NAMESPACE")
		if ok {
			complianceOperatorNamespace = ns
		} else {
			ns, ok := os.LookupEnv("WATCH_NAMESPACE")
			if ok {
				complianceOperatorNamespace = ns
			}
		}
	} else {
		nsBytes, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			return
		}
		complianceOperatorNamespace = strings.TrimSpace(string(nsBytes))
	}
}

func isRunModeLocal() bool {
	return os.Getenv(ForceRunModeEnv) == string(LocalRunMode)
}

// GetComplianceOperatorNamespace gets the namespace that the operator is
// currently running on.
func GetComplianceOperatorNamespace() string {
	return complianceOperatorNamespace
}

// GetComplianceOperatorName gets the name that the operator is
// currently running with.
func GetComplianceOperatorName() string {
	return complianceOperatorName
}
