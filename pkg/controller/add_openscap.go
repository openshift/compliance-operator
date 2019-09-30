package controller

import (
	"github.com/jhrozek/openscap-operator/pkg/controller/openscap"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, openscap.Add)
}
