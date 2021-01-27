package compliancesuite

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestCompliancesuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Compliancesuite Suite")
}
