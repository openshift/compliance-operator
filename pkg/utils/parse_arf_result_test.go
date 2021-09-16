package utils

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	igntypes "github.com/coreos/ignition/v2/config/v3_1/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	mcfgcommon "github.com/openshift/machine-config-operator/pkg/controller/common"
)

func countResultItems(resultList []*ParseResult) (int, int) {
	if resultList == nil {
		return 0, 0
	}

	var nChecks, nRems int

	for _, res := range resultList {
		if res == nil {
			continue
		}

		if res.Remediations != nil {
			nRems++
		}

		if res.CheckResult != nil {
			nChecks++
		}
	}

	return nChecks, nRems
}

var _ = Describe("XCCDF parser", func() {
	const (
		totalRemediations = 183
		totalChecks       = 229
	)

	var (
		xccdf                  io.Reader
		ds                     io.Reader
		schema                 *runtime.Scheme
		resultsFilename        string
		dsFilenameWrongFormate string
		dsFilename             string
		resultList             []*ParseResult
		nChecks                int
		nRems                  int
		err                    error
	)

	Describe("Testing for wrongly formatted Remediation", func() {

		mcInstance := &mcfgv1.MachineConfig{}
		schema = scheme.Scheme
		schema.AddKnownTypes(mcfgv1.SchemeGroupVersion, mcInstance)
		resultsFilename = "../../tests/data/xccdf-result-remdiation-templating.xml"
		dsFilenameWrongFormate = "../../tests/data/ds-input-for-remediation-value-wrong-formate.xml"
		// I added {{.var_f[]ake_value|urlquery}} on line 51691 to test out the handling for wrongly template format
		xccdf, err = os.Open(resultsFilename)
		Expect(err).NotTo(HaveOccurred())
		ds, err = os.Open(dsFilenameWrongFormate)
		Expect(err).NotTo(HaveOccurred())
		dsDom, err := ParseContent(ds)
		Expect(err).NotTo(HaveOccurred())
		resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf)

		Context("Make Sure it handles the Wrongly formatted Remdiation TemplateF", func() {
			//It will parse all other checks and remediation as normal
			//It will not create remediation for the wrong formate one
			expRule := "rule_auditd_data_retention_max_log"
			nChecks, nRems = countResultItems(resultList)
			It("Should throw an Wrong Template formate error with rule name", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expRule))
			})
			It("Should still have the result list", func() {
				Expect(resultList).NotTo(BeEmpty())
			})
			It("Should have one less then original Remdiaiton ", func() {
				Expect(nRems).To(Equal(totalRemediations - 1))
			})
		})

	})

	Describe("Load the XCCDF and the DS separately for Remdiation templating", func() {
		BeforeEach(func() {
			mcInstance := &mcfgv1.MachineConfig{}
			schema = scheme.Scheme
			schema.AddKnownTypes(mcfgv1.SchemeGroupVersion, mcInstance)
			resultsFilename = "../../tests/data/xccdf-result-remdiation-templating.xml"
			dsFilename = "../../tests/data/ds-input-for-remediation-value.xml"
			//the ds-input-for-remediation-value is generated from newly modified content build
			//it has data:,{{ ..23%0A%23%20This%20file%20controls%20the%20configuratio... }} formate

		})
		JustBeforeEach(func() {
			xccdf, err = os.Open(resultsFilename)
			Expect(err).NotTo(HaveOccurred())

			ds, err = os.Open(dsFilename)
			Expect(err).NotTo(HaveOccurred())
			dsDom, err := ParseContent(ds)
			Expect(err).NotTo(HaveOccurred())
			resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultList).NotTo(BeEmpty())

		})

		Context("Valid XCCDF", func() {
			It("Should parse the XCCDF without errors", func() {
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Check if parsing will generate remediation with correct template", func() {
			var (
				rem     *compv1alpha1.ComplianceRemediation
				expName string
			)

			expValueUsedAnnotation := "var-postfix-relayhost,var-auditd-max-log-file" //expect found and used value
			expUnsetValueAnnotation := "var-fake-second-value,var-fake-value"         //expect not found value
			expRequiredValueAnnotation := "var-some-required-value"
			BeforeEach(func() {
				expName = "testScan-auditd-data-retention-max-log-file"
				for i := range resultList {
					if resultList[i].Remediations != nil {
						if resultList[i].Remediations[0].Name == expName {
							rem = resultList[i].Remediations[0]
							break
						}
					}
				}
				Expect(rem).ToNot(BeNil())
			})

			It("The Remediation should be the correct testing remediation", func() {
				Expect(rem.Name).To(Equal(expName))
			})

			It("The remdiation should have correct Value-Used annotation", func() {
				Expect(rem.Annotations[compv1alpha1.RemediationValueUsedAnnotation]).To(Equal(expValueUsedAnnotation))
			})

			It("The remdiation should have correct Value-Not-Found/Set annotation", func() {
				Expect(rem.Annotations[compv1alpha1.RemediationUnsetValueAnnotation]).To(Equal(expUnsetValueAnnotation))
			})

			It("The remdiation should have correct Required-Value annotation", func() {
				Expect(rem.Annotations[compv1alpha1.RemediationValueRequiredAnnotation]).To(Equal(expRequiredValueAnnotation))
			})

		})

		Context("Check if parsing will generate remediation with correct template mutiply lines", func() {
			var (
				rem     *compv1alpha1.ComplianceRemediation
				expName string
			)

			expValueUsedAnnotation := "var-multiple-time-servers" //expect found and used value
			expRequiredValueAnnotation := "var-multiple-time-servers"
			expNTPServersSetting := `Server 0.fedora.pool.ntp.org
Server 1.fedora.pool.ntp.org
Server 2.fedora.pool.ntp.org
Server 3.fedora.pool.ntp.org`
			expNTPServersSettingUrl := url.PathEscape(expNTPServersSetting)
			BeforeEach(func() {
				expName = "testScan-chronyd-or-ntpd-specify-multiple-servers"
				for i := range resultList {
					if resultList[i].Remediations != nil {
						if resultList[i].Remediations[0].Name == expName {
							rem = resultList[i].Remediations[0]
							break
						}
					}
				}
				Expect(rem).ToNot(BeNil())
			})

			It("The Remediation should be the correct testing remediation", func() {
				Expect(rem.Name).To(Equal(expName))
			})

			It("The remdiation should have correct Value-Used annotation", func() {
				Expect(rem.Annotations[compv1alpha1.RemediationValueUsedAnnotation]).To(Equal(expValueUsedAnnotation))
			})

			It("The remdiation should have correct Required-Value annotation", func() {
				Expect(rem.Annotations[compv1alpha1.RemediationValueRequiredAnnotation]).To(Equal(expRequiredValueAnnotation))
			})

			It("The remediation should have correct ntp servers", func() {
				//print remediation machine config into variable
				MCContent := fmt.Sprintf("%s", rem.Spec.Current.Object.Object)
				Expect(strings.Contains(MCContent, expNTPServersSettingUrl)).To(Equal(true))
			})

		})

	})

	Describe("Test for Check Result Variable Association ", func() {
		BeforeEach(func() {
			mcInstance := &mcfgv1.MachineConfig{}
			schema = scheme.Scheme
			schema.AddKnownTypes(mcfgv1.SchemeGroupVersion, mcInstance)
			resultsFilename = "../../tests/data/xccdf-result-remdiation-templating.xml"
			dsFilename = "../../tests/data/ds-input-for-remediation-value.xml"
		})

		JustBeforeEach(func() {
			xccdf, err = os.Open(resultsFilename)
			Expect(err).NotTo(HaveOccurred())

			ds, err = os.Open(dsFilename)
			Expect(err).NotTo(HaveOccurred())
			dsDom, err := ParseContent(ds)
			Expect(err).NotTo(HaveOccurred())
			resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf)
			Expect(resultList).NotTo(BeEmpty())
			nChecks, nRems = countResultItems(resultList)
		})

		Context("Check if the check result has correct value used attribute with one variable", func() {
			const (
				expCheckResultName = "testScan-auditd-data-retention-max-log-file"
				expValue           = "var-auditd-max-log-file"
			)

			var (
				check *compv1alpha1.ComplianceCheckResult
			)

			BeforeEach(func() {
				for i := range resultList {
					if resultList[i].CheckResult != nil && resultList[i].CheckResult.Name == expCheckResultName {
						check = resultList[i].CheckResult
						break
					}
				}
			})

			It("Should have the expected value List", func() {
				Expect(len(check.ValuesUsed)).To(Equal(1))
				Expect(check.ValuesUsed[0]).To(Equal(expValue))
			})
		})

		Context("Check if the check result has correct value used attribute with no variable", func() {
			const (
				expCheckResultName = "testScan-grub2-uefi-password"
			)

			var (
				check *compv1alpha1.ComplianceCheckResult
			)

			BeforeEach(func() {
				for i := range resultList {
					if resultList[i].CheckResult != nil && resultList[i].CheckResult.Name == expCheckResultName {
						check = resultList[i].CheckResult
						break
					}
				}
			})

			It("Should have empty value List", func() {
				Expect(len(check.ValuesUsed)).To(Equal(0))
			})
		})

	})
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
			resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf)
			Expect(resultList).NotTo(BeEmpty())
			nChecks, nRems = countResultItems(resultList)
		})

		Context("Valid XCCDF", func() {
			It("Should parse the XCCDF without errors", func() {
				Expect(err).NotTo(HaveOccurred())
			})
			It("Should return expected remediations", func() {
				Expect(nRems).To(Equal(totalRemediations))
			})
			It("Should return expected checks", func() {
				Expect(nChecks).To(Equal(totalChecks))
			})
		})

		Context("First check metadata", func() {
			const (
				expID           = "xccdf_org.ssgproject.content_rule_selinux_policytype"
				expDescription  = "Configure SELinux Policy"
				expInstructions = "Check the file /etc/selinux/config and ensure the following line appears:\nSELINUXTYPE="
			)

			var (
				check *compv1alpha1.ComplianceCheckResult
			)

			BeforeEach(func() {
				for i := range resultList {
					if resultList[i].CheckResult != nil && resultList[i].CheckResult.ID == expID {
						check = resultList[i].CheckResult
						break
					}
				}
				Expect(check).ToNot(BeNil())
			})

			It("Should have the expected status", func() {
				Expect(check.Status).To(Equal(compv1alpha1.CheckResultPass))
			})

			It("Should have the expected severity", func() {
				Expect(check.Severity).To(Equal(compv1alpha1.CheckResultSeverityMedium))
			})

			It("Should have the expected description", func() {
				Expect(check.Description).To(HavePrefix(expDescription))
			})

			It("Should have the expected instructions", func() {
				Expect(check.Instructions).To(HavePrefix(expInstructions))
			})
		})

		Context("First remediation type", func() {
			var (
				rem     *compv1alpha1.ComplianceRemediation
				expName string
			)

			BeforeEach(func() {
				for i := range resultList {
					if resultList[i].Remediations != nil {
						rem = resultList[i].Remediations[0]
						break
					}
				}
				Expect(rem).ToNot(BeNil())
				expName = "testScan-no-direct-root-logins"
			})

			It("Should have the expected name", func() {
				Expect(rem.Name).To(Equal(expName))
			})
			It("Should be a MC", func() {
				Expect(rem.Spec.Current.Object.GetKind()).To(Equal("MachineConfig"))
			})

			Context("MC files", func() {
				var (
					mcFiles []igntypes.File
				)

				BeforeEach(func() {
					mcfg, _ := ParseMachineConfig(rem, rem.Spec.Current.Object)
					ignRaw, _ := mcfgcommon.IgnParseWrapper(mcfg.Spec.Config.Raw)
					parsedIgn := ignRaw.(igntypes.Config)
					mcFiles = parsedIgn.Storage.Files
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
					resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf)
					Expect(err).NotTo(HaveOccurred())
					Expect(nRems).To(Equal(totalRemediations))
					Expect(nChecks).To(Equal(totalChecks))
				})

				Ω(runtime.Seconds()).Should(BeNumerically("<", 3.0), "ParseRemediationsFromArf() shouldn't take too long.")
			}, 100)
		})
	})
})
