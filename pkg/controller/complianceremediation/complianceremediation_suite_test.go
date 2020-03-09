package complianceremediation_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestComplianceRemediation(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ComplianceRemediation Suite")
}
