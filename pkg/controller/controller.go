package controller

import (
	"github.com/openshift/compliance-operator/pkg/controller/metrics"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// AddToManagerFuncs is a list of functions to add all Controllers to the Manager
var AddToManagerFuncs []func(manager.Manager, *metrics.Metrics) error

// AddToManager adds all Controllers to the Manager
func AddToManager(m manager.Manager, met *metrics.Metrics) error {
	// Add metrics Startup to the manager
	if err := m.Add(met); err != nil {
		return err
	}

	for _, f := range AddToManagerFuncs {
		if err := f(m, met); err != nil {
			return err
		}
	}
	return nil
}
