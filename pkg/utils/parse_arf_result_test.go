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

	})
})
