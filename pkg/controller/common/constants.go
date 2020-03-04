package common

import (
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
)

var complianceOperatorNamespace = "openshift-compliance"

func init() {
	ns, err := k8sutil.GetOperatorNamespace()
	if err == nil {
		complianceOperatorNamespace = ns
	}
}

func GetComplianceOperatorNamespace() string {
	return complianceOperatorNamespace
}
