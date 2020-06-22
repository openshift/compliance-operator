package scansettingbinding_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestScanSettingBinding(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ScanSettingBinding Suite")
}
