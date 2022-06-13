package utils

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
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
		manualRules := []string{}
		resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf, manualRules)

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
			manualRules := []string{}
			resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf, manualRules)
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
			/*
				kind: MachineConfig
				metadata:
				annotations:
					complianceascode.io/value-input-required: var_some_required_value
				spec:
					config:
						ignition:
							version: 3.1.0
					storage:
						files:
						- contents:
				  			source: data:,{{ %7B%7B.var_fake_second_value%7D%7D%0A%7B%7B.var_fake_value%7D%7D%0A%7B%7B.var_postfix_relayhost%7D%7D%0A%0Alocal_events%20%3D%20yes%0Awrite_logs%20%3D%20yes%0Alog_file%20%3D%20%2Fvar%2Flog%2Faudit%2Faudit.log%0Alog_group%20%3D%20root%0Alog_format%20%3D%20ENRICHED%0Aflush%20%3D%20incremental_async%0Afreq%20%3D%2050%0Amax_log_file%20%3D%20%7B%7B.var_auditd_max_log_file%7D%7D%0Anum_logs%20%3D%205%0Apriority_boost%20%3D%204%0Aname_format%20%3D%20hostname%0A%23%23name%20%3D%20mydomain%0Amax_log_file_action%20%3D%20rotate%0Aspace_left%20%3D%20100%0Aspace_left_action%20%3D%20syslog%0Averify_email%20%3D%20yes%0Aaction_mail_acct%20%3D%20root%0Aadmin_space_left%20%3D%2050%0Aadmin_space_left_action%20%3D%20syslog%0Adisk_full_action%20%3D%20syslog%0Adisk_error_action%20%3D%20syslog%0Ause_libwrap%20%3D%20yes%0A%23%23tcp_listen_port%20%3D%2060%0Atcp_listen_queue%20%3D%205%0Atcp_max_per_addr%20%3D%201%0A%23%23tcp_client_ports%20%3D%201024-65535%0Atcp_client_max_idle%20%3D%200%0Atransport%20%3D%20TCP%0Akrb5_principal%20%3D%20auditd%0A%23%23krb5_key_file%20%3D%20%2Fetc%2Faudit%2Faudit.key%0Adistribute_network%20%3D%20no%0Aq_depth%20%3D%20400%0Aoverflow_action%20%3D%20syslog%0Amax_restarts%20%3D%2010%0Aplugin_dir%20%3D%20%2Fetc%2Faudit%2Fplugins.d }}
						mode: {{.var_file_mode}}
						path: /etc/audit/auditd.conf
						overwrite: true
			*/
			expValueUsedAnnotation := "var-postfix-relayhost,var-auditd-max-log-file"       //expect found and used value
			expUnsetValueAnnotation := "var-fake-second-value,var-fake-value,var-file-mode" //expect not found value
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
			manualRules := []string{}
			resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf, manualRules)
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

	Describe("Test for manual Rules", func() {
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
			manualRules := []string{}
			manualRules = append(manualRules, "rhcos4-auditd-data-retention-space-left")
			resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf, manualRules)
			Expect(resultList).NotTo(BeEmpty())
		})

		Context("Check if the check result has correct value used attribute with one variable", func() {
			const (
				expCheckResultName = "testScan-auditd-data-retention-space-left"
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
			Context("Valid rule result", func() {
				It("Should have the correct rule", func() {
					Expect(check.Name).To(Equal(expCheckResultName))

				})
				It("Should have the expected check result status", func() {
					Expect(check.Status).To(Equal(compv1alpha1.CheckResultManual))
				})
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
			manualRules := []string{}
			resultList, err = ParseResultsFromContentAndXccdf(schema, "testScan", "testNamespace", dsDom, xccdf, manualRules)
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

	Describe("Testing for parseValues", func() {
		var value_dic = map[string]string{
			"the_value_1": "3600,1200,3122",
			"the_value_2": "1111",
			"the_value_3": "2222",
			"the_value_4": "3333",
			"var_kubelet_evictionhard_imagefs_available": "10%",
			"var_version": "3.1.0",
			"var_servers": "server1,server2,server3",
		}

		var usedVals []string
		var missingVals []string
		var processedContent string
		var MachineConfig string
		Context("Contents with only url-encoded template content", func() {

			expUsedVals := []string{"the-value-1", "the-value-2"}
			expMissingVals := []string{"the-value-not-defined"}

			BeforeEach(func() {
				/*
					The content of following url-encoded data:
					test1 := {{.the_value_1}}
					test2 := {{.the_value_2}}
					test_not_defined := {{.the_value_not_defined}}
				*/
				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
			kind: MachineConfig
			spec:
			  config:
				ignition:
				  version: 3.1.0
				storage:
				  files:
				  - contents:
					  source: data:,{{ test1%20%3A%3D%20%7B%7B.the_value_1%7D%7D%0Atest2%20%3A%3D%20%7B%7B.the_value_2%7D%7D%0Atest_not_defined%20%3A%3D%20%7B%7B.the_value_not_defined%7D%7D }}
					mode: 420
					overwrite: true
					path: /etc/chrony.conf`

				_, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)
			})
			It("Should parse without error", func() {
				Expect(err).To(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(Equal(expMissingVals))
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(Equal(expUsedVals))
			})
		})

		Context("Contents with only url-encoded template content extra space at beginning", func() {

			expUsedVals := []string{"the-value-1", "the-value-2"}
			expMissingVals := []string{"the-value-not-defined"}

			BeforeEach(func() {
				/*
					The content of following url-encoded data:
					test1 := {{.the_value_1}}
					test2 := {{.the_value_2}}
					test_not_defined := {{.the_value_not_defined}}
				*/
				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
			kind: MachineConfig
			spec:
			  config:
				ignition:
				  version: 3.1.0
				storage:
				  files:
				  - contents:
					  source: data:,{{   test1%20%3A%3D%20%7B%7B.the_value_1%7D%7D%0Atest2%20%3A%3D%20%7B%7B.the_value_2%7D%7D%0Atest_not_defined%20%3A%3D%20%7B%7B.the_value_not_defined%7D%7D }}
					mode: 420
					overwrite: true
					path: /etc/chrony.conf`

				_, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)
			})
			It("Should parse without error", func() {
				Expect(err).To(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(Equal(expMissingVals))
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(Equal(expUsedVals))
			})
		})

		Context("Contents with wrong formatted url-encoded template content", func() {

			BeforeEach(func() {
				// an extra space is added in url-encoded data
				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
			kind: MachineConfig
			spec:
			  config:
				ignition:
				  version: 3.1.0
				storage:
				  files:
				  - contents:
					  source: data:,{{ test1%20%3A%3D%20%7B%7B.t he_value_1%7D%7D%0Atest2%20%3A%3D%20%7B%7B.the_value_2%7D%7D%0Atest_not_defined%20%3A%3D%20%7B%7B.the_value_not_defined%7D%7D }}
					mode: 420
					overwrite: true
					path: /etc/chrony.conf`

				_, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)
			})
			It("Should parse with error", func() {
				Expect(err).NotTo(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(BeEmpty())
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(BeEmpty())
			})
		})

		Context("Contents with non-url-encoded template content", func() {

			expUsedVals := []string{"var-kubelet-evictionhard-imagefs-available"}

			BeforeEach(func() {

				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
			kind: KubeletConfig
			spec:
			  kubeletConfig:
			  	evictionHard:
				  imagefs.available: {{.var_kubelet_evictionhard_imagefs_available}}`

				_, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)
			})

			It("Should parse without error", func() {
				Expect(err).To(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(BeEmpty())
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(Equal(expUsedVals))
			})
		})

		Context("Contents with non-url-encoded template content loop array variable", func() {

			expUsedVals := []string{"var-servers", "the-value-2"}
			BeforeEach(func() {

				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
			kind: KubeletConfig
			spec:
				  {{$var_servers:=.var_servers}}
				  {{$the_value_2:=.the_value_2}}
				  {{range $element:=$var_servers|toArrayByComma}}server {{$element}} minpoll 4 maxpoll {{$the_value_2}}
				  {{end}}`

				_, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)

			})

			It("Should parse without error", func() {
				Expect(err).To(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(BeEmpty())
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(Equal(expUsedVals))
			})
		})

		Context("Contents with wrong non-url-encoded template content", func() {

			BeforeEach(func() {

				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
			kind: KubeletConfig
			spec:
			  kubeletConfig:
			  	evictionHard:
				  imagefs.available: {{.var_kubelet_evi ctionhard_imagefs_available}}`

				_, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)
			})

			It("Should parse with error", func() {
				Expect(err).NotTo(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(BeEmpty())
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(BeEmpty())
			})
		})

		Context("Contents with url-encoded template content and non-urlencoded template content", func() {

			expUsedVals := []string{"the-value-1", "the-value-2", "var-version"}
			expMissingVals := []string{"the-value-not-defined", "var-file"}

			BeforeEach(func() {

				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
			kind: MachineConfig
			spec:
			  config:
				ignition:
				  version: {{.var_version}}
				storage:
				  files: {{.var_file}}
				  - contents:
					  source: data:,{{ test1%20%3A%3D%20%7B%7B.the_value_1%7D%7D%0Atest2%20%3A%3D%20%7B%7B.the_value_2%7D%7D%0Atest_not_defined%20%3A%3D%20%7B%7B.the_value_not_defined%7D%7D }}
					mode: 420
					overwrite: true
					path: /etc/chrony.conf`

				_, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)
			})
			It("Should parse without error", func() {
				Expect(err).To(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(Equal(expMissingVals))
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(Equal(expUsedVals))
			})
		})

		Context("Contents without template", func() {

			BeforeEach(func() {

				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
			kind: MachineConfig
			spec:
			  config:
				ignition:
				  version: 3.1.0
				storage:
				  files: 
				  - contents:
					  source: data:,test1%20%3A%3D%20%7B%7B.the_value_1%7D%7D%0Atest2%20%3A%3D%20%7B%7B.the_value_2%7D%7D%0Atest_not_defined%20%3A%3D%20%7B%7B.the_value_not_defined%7D%7D
					mode: 420
					overwrite: true
					path: /etc/chrony.conf`

				processedContent, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)
			})
			It("Should parse without error", func() {
				Expect(err).To(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(BeEmpty())
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(BeEmpty())
			})

			It("Should have orignial content", func() {
				Expect(processedContent).To(Equal(MachineConfig))
			})
		})

		Context("Test remediation for sshd-idle-timeout", func() {
			BeforeEach(func() {

				MachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
				kind: MachineConfig
				spec:
				  config:
					ignition:
					  version: 3.1.0
					storage:
					  files:
					  - contents:
						  source: data:,{{ %0A%23%09%24OpenBSD%3A%20sshd_config%2Cv%201.103%202018/04/09%2020%3A41%3A22%20tj%20Exp%20%24%0A%0A%23%20This%20is%20the%20sshd%20server%20system-wide%20configuration%20file.%20%20See%0A%23%20sshd_config%285%29%20for%20more%20information.%0A%0A%23%20This%20sshd%20was%20compiled%20with%20PATH%3D/usr/local/bin%3A/usr/bin%3A/usr/local/sbin%3A/usr/sbin%0A%0A%23%20The%20strategy%20used%20for%20options%20in%20the%20default%20sshd_config%20shipped%20with%0A%23%20OpenSSH%20is%20to%20specify%20options%20with%20their%20default%20value%20where%0A%23%20possible%2C%20but%20leave%20them%20commented.%20%20Uncommented%20options%20override%20the%0A%23%20default%20value.%0A%0A%23%20If%20you%20want%20to%20change%20the%20port%20on%20a%20SELinux%20system%2C%20you%20have%20to%20tell%0A%23%20SELinux%20about%20this%20change.%0A%23%20semanage%20port%20-a%20-t%20ssh_port_t%20-p%20tcp%20%23PORTNUMBER%0A%23%0A%23Port%2022%0A%23AddressFamily%20any%0A%23ListenAddress%200.0.0.0%0A%23ListenAddress%20%3A%3A%0A%0AHostKey%20/etc/ssh/ssh_host_rsa_key%0AHostKey%20/etc/ssh/ssh_host_ecdsa_key%0AHostKey%20/etc/ssh/ssh_host_ed25519_key%0A%0A%23%20Ciphers%20and%20keying%0ARekeyLimit%20512M%201h%0A%0A%23%20System-wide%20Crypto%20policy%3A%0A%23%20This%20system%20is%20following%20system-wide%20crypto%20policy.%20The%20changes%20to%0A%23%20Ciphers%2C%20MACs%2C%20KexAlgoritms%20and%20GSSAPIKexAlgorithsm%20will%20not%20have%20any%0A%23%20effect%20here.%20They%20will%20be%20overridden%20by%20command-line%20options%20passed%20on%0A%23%20the%20server%20start%20up.%0A%23%20To%20opt%20out%2C%20uncomment%20a%20line%20with%20redefinition%20of%20%20CRYPTO_POLICY%3D%0A%23%20variable%20in%20%20/etc/sysconfig/sshd%20%20to%20overwrite%20the%20policy.%0A%23%20For%20more%20information%2C%20see%20manual%20page%20for%20update-crypto-policies%288%29.%0A%0A%23%20Logging%0A%23SyslogFacility%20AUTH%0ASyslogFacility%20AUTHPRIV%0A%23LogLevel%20INFO%0A%0A%23%20Authentication%3A%0A%0A%23LoginGraceTime%202m%0APermitRootLogin%20no%0AStrictModes%20yes%0A%23MaxAuthTries%206%0A%23MaxSessions%2010%0A%0APubkeyAuthentication%20yes%0A%0A%23%20The%20default%20is%20to%20check%20both%20.ssh/authorized_keys%20and%20.ssh/authorized_keys2%0A%23%20but%20this%20is%20overridden%20so%20installations%20will%20only%20check%20.ssh/authorized_keys%0AAuthorizedKeysFile%09.ssh/authorized_keys%0A%0A%23AuthorizedPrincipalsFile%20none%0A%0A%23AuthorizedKeysCommand%20none%0A%23AuthorizedKeysCommandUser%20nobody%0A%0A%23%20For%20this%20to%20work%20you%20will%20also%20need%20host%20keys%20in%20/etc/ssh/ssh_known_hosts%0AHostbasedAuthentication%20no%0A%23%20Change%20to%20yes%20if%20you%20don%27t%20trust%20~/.ssh/known_hosts%20for%0A%23%20HostbasedAuthentication%0AIgnoreUserKnownHosts%20yes%0A%23%20Don%27t%20read%20the%20user%27s%20~/.rhosts%20and%20~/.shosts%20files%0AIgnoreRhosts%20yes%0A%0A%23%20To%20disable%20tunneled%20clear%20text%20passwords%2C%20change%20to%20no%20here%21%0A%23PasswordAuthentication%20yes%0APermitEmptyPasswords%20no%0APasswordAuthentication%20no%0A%0A%23%20Change%20to%20no%20to%20disable%20s/key%20passwords%0A%23ChallengeResponseAuthentication%20yes%0AChallengeResponseAuthentication%20no%0A%0A%23%20Kerberos%20options%0AKerberosAuthentication%20no%0A%23KerberosOrLocalPasswd%20yes%0A%23KerberosTicketCleanup%20yes%0A%23KerberosGetAFSToken%20no%0A%23KerberosUseKuserok%20yes%0A%0A%23%20GSSAPI%20options%0AGSSAPIAuthentication%20no%0AGSSAPICleanupCredentials%20no%0A%23GSSAPIStrictAcceptorCheck%20yes%0A%23GSSAPIKeyExchange%20no%0A%23GSSAPIEnablek5users%20no%0A%0A%23%20Set%20this%20to%20%27yes%27%20to%20enable%20PAM%20authentication%2C%20account%20processing%2C%0A%23%20and%20session%20processing.%20If%20this%20is%20enabled%2C%20PAM%20authentication%20will%0A%23%20be%20allowed%20through%20the%20ChallengeResponseAuthentication%20and%0A%23%20PasswordAuthentication.%20%20Depending%20on%20your%20PAM%20configuration%2C%0A%23%20PAM%20authentication%20via%20ChallengeResponseAuthentication%20may%20bypass%0A%23%20the%20setting%20of%20%22PermitRootLogin%20without-password%22.%0A%23%20If%20you%20just%20want%20the%20PAM%20account%20and%20session%20checks%20to%20run%20without%0A%23%20PAM%20authentication%2C%20then%20enable%20this%20but%20set%20PasswordAuthentication%0A%23%20and%20ChallengeResponseAuthentication%20to%20%27no%27.%0A%23%20WARNING%3A%20%27UsePAM%20no%27%20is%20not%20supported%20in%20Fedora%20and%20may%20cause%20several%0A%23%20problems.%0AUsePAM%20yes%0A%0A%23AllowAgentForwarding%20yes%0A%23AllowTcpForwarding%20yes%0A%23GatewayPorts%20no%0AX11Forwarding%20yes%0A%23X11DisplayOffset%2010%0A%23X11UseLocalhost%20yes%0A%23PermitTTY%20yes%0A%0A%23%20It%20is%20recommended%20to%20use%20pam_motd%20in%20/etc/pam.d/sshd%20instead%20of%20PrintMotd%2C%0A%23%20as%20it%20is%20more%20configurable%20and%20versatile%20than%20the%20built-in%20version.%0APrintMotd%20no%0A%0APrintLastLog%20yes%0A%23TCPKeepAlive%20yes%0APermitUserEnvironment%20no%0ACompression%20no%0AClientAliveInterval%20600%0AClientAliveCountMax%200%0A%23UseDNS%20no%0A%23PidFile%20/var/run/sshd.pid%0A%23MaxStartups%2010%3A30%3A100%0A%23PermitTunnel%20no%0A%23ChrootDirectory%20none%0A%23VersionAddendum%20none%0A%0A%23%20no%20default%20banner%20path%0ABanner%20/etc/issue%0A%0A%23%20Accept%20locale-related%20environment%20variables%0AAcceptEnv%20LANG%20LC_CTYPE%20LC_NUMERIC%20LC_TIME%20LC_COLLATE%20LC_MONETARY%20LC_MESSAGES%0AAcceptEnv%20LC_PAPER%20LC_NAME%20LC_ADDRESS%20LC_TELEPHONE%20LC_MEASUREMENT%0AAcceptEnv%20LC_IDENTIFICATION%20LC_ALL%20LANGUAGE%0AAcceptEnv%20XMODIFIERS%0A%0A%23%20override%20default%20of%20no%20subsystems%0ASubsystem%09sftp%09/usr/libexec/openssh/sftp-server%0A%0A%23%20Example%20of%20overriding%20settings%20on%20a%20per-user%20basis%0A%23Match%20User%20anoncvs%0A%23%09X11Forwarding%20no%0A%23%09AllowTcpForwarding%20no%0A%23%09PermitTTY%20no%0A%23%09ForceCommand%20cvs%20server%0A%0AUsePrivilegeSeparation%20sandbox }}
						mode: 0600
						path: /etc/ssh/sshd_config
						overwrite: true`

				_, usedVals, missingVals, err = parseValues(MachineConfig, value_dic)
			})
			It("Should parse with error", func() {
				Expect(err).To(BeNil())
			})

			It("Should have correct missing values list", func() {
				Expect(missingVals).To(BeEmpty())
			})

			It("Should have correct used values list", func() {
				Expect(usedVals).To(BeEmpty())
			})
		})

	})
})
