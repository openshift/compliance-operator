package compliancescan_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestComplianceScan(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ComplianceScan Suite")
}
