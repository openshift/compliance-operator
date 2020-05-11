package common

import (
	"os"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
)

var complianceOperatorNamespace = "openshift-compliance"

func init() {
	if isRunModeLocal() {
		ns, ok := os.LookupEnv("WATCH_NAMESPACE")
		if ok {
			complianceOperatorNamespace = ns
		}
	} else {
		ns, err := k8sutil.GetOperatorNamespace()
		if err == nil {
			complianceOperatorNamespace = ns
		}
	}
}

func isRunModeLocal() bool {
	return os.Getenv(k8sutil.ForceRunModeEnv) == string(k8sutil.LocalRunMode)
}

// GetComplianceOperatorNamespace gets the namespace that the operator is
// currently running on.
func GetComplianceOperatorNamespace() string {
	return complianceOperatorNamespace
}
