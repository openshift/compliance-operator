package controller

import (
	"github.com/ComplianceAsCode/compliance-operator/pkg/controller/compliancesuite"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, compliancesuite.Add)
}
