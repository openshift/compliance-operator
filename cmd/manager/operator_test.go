package main

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/compliance-operator/pkg/controller/metrics"
)

var _ = Describe("Operator Startup Function tests", func() {
	Context("Service Monitor Creation", func() {
		When("Installing to non-controlled namespace", func() {
			It("ServiceMonitor is generated with the proper TLSConfig ServerName", func() {
				metricService := operatorMetricService("foobar")
				sm := generateOperatorServiceMonitor(metricService, "foobar")
				controllerMetricServiceFound := false
				for _, ep := range sm.Spec.Endpoints {
					if ep.Port == metrics.ControllerMetricsServiceName && ep.TLSConfig != nil {
						Expect(ep.TLSConfig.ServerName).To(BeEquivalentTo("metrics.foobar.svc"))
						controllerMetricServiceFound = true
					}
				}
				Expect(controllerMetricServiceFound).To(BeTrue())
			})
		})
	})
})
