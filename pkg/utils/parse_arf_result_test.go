package utils

import (
	"io"
	"os"

	igntypes "github.com/coreos/ignition/config/v2_2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

var _ = Describe("XCCDF parser", func() {
	const (
		totalRemediations = 5
		totalChecks       = 464
	)

	var (
		xccdf           io.Reader
		ds              io.Reader
		schema          *runtime.Scheme
		resultsFilename string
		dsFilename      string
		remList         []*compv1alpha1.ComplianceRemediation
		checkList       []*compv1alpha1.ComplianceCheck
		err             error
	)

	Describe("Load the XCCDF and the DS separately", func() {
		BeforeEach(func() {
			mcInstance := &mcfgv1.MachineConfig{}
			schema = scheme.Scheme
			schema.AddKnownTypes(mcfgv1.SchemeGroupVersion, mcInstance)
			resultsFilename = "../../tests/data/xccdf-result.xml"
			dsFilename = "../../tests/data/ds-input.xml"
		})

		JustBeforeEach(func() {
			xccdf, err = os.Open(resultsFilename)
			Expect(err).NotTo(HaveOccurred())

			ds, err = os.Open(dsFilename)
			Expect(err).NotTo(HaveOccurred())
			dsDom, err := ParseContent(ds)
			Expect(err).NotTo(HaveOccurred())
			checkList, remList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf)
		})

		Context("Valid XCCDF", func() {
			It("Should parse the XCCDF without errors", func() {
				Expect(err).NotTo(HaveOccurred())
			})
			It("Should return exactly five remediations", func() {
				Expect(remList).To(HaveLen(totalRemediations))
			})
			It("Should return exactly 464 checks", func() {
				Expect(checkList).To(HaveLen(totalChecks))
			})
		})

		Context("First remediation type", func() {
			var (
				rem     *compv1alpha1.ComplianceRemediation
				expName string
			)

			BeforeEach(func() {
				rem = remList[0]
				expName = "testScan-no-direct-root-logins"
			})

			It("Should have the expected name", func() {
				Expect(rem.Name).To(Equal(expName))
			})
			It("Should be a MC", func() {
				Expect(rem.Spec.Type).To(Equal(compv1alpha1.McRemediation))
			})

			Context("MC files", func() {
				var (
					mcFiles []igntypes.File
				)

				BeforeEach(func() {
					mcFiles = rem.Spec.MachineConfigContents.Spec.Config.Storage.Files
				})

				It("Should define one file", func() {
					Expect(mcFiles).To(HaveLen(1))
				})
				It("Should define the expected file", func() {
					Expect(mcFiles[0].Path).To(Equal("/etc/securetty"))
				})
			})
		})
	})

	Describe("Benchmark loading the XCCFD and the DS", func() {
		BeforeEach(func() {
			mcInstance := &mcfgv1.MachineConfig{}
			schema = scheme.Scheme
			schema.AddKnownTypes(mcfgv1.SchemeGroupVersion, mcInstance)
			resultsFilename = "../../tests/data/xccdf-result.xml"
			dsFilename = "../../tests/data/ds-input.xml"
		})

		JustBeforeEach(func() {
			xccdf, err = os.Open(resultsFilename)
			Expect(err).NotTo(HaveOccurred())

			ds, err = os.Open(dsFilename)
			Expect(err).NotTo(HaveOccurred())

		})

		Context("Valid XCCDF and DS with remediations", func() {
			Measure("Should parse the XCCDF and DS without errors", func(b Benchmarker) {
				runtime := b.Time("runtime", func() {
					dsDom, err := ParseContent(ds)
					Expect(err).NotTo(HaveOccurred())
					checkList, remList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf)
					Expect(err).NotTo(HaveOccurred())
					Expect(remList).To(HaveLen(totalRemediations))
					Expect(checkList).To(HaveLen(totalChecks))
				})

				Ω(runtime.Seconds()).Should(BeNumerically("<", 3.0), "ParseRemediationsFromArf() shouldn't take too long.")
			}, 100)
		})
	})
})
