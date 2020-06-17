package utils

import (
	"github.com/clarketm/json"
	igntypes "github.com/coreos/ignition/config/v2_2/types"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Testing parse results diff", func() {
	var (
		remService    *compv1alpha1.ComplianceRemediation
		remService2   *compv1alpha1.ComplianceRemediation
		checkService  *compv1alpha1.ComplianceCheckResult
		checkService2 *compv1alpha1.ComplianceCheckResult
		pRes          *ParseResult
		pRes2         *ParseResult
		oldList       []*ParseResult
		newList       []*ParseResult
	)

	BeforeEach(func() {
		ignConfig := igntypes.Config{
			Systemd: igntypes.Systemd{
				Units: []igntypes.Unit{
					{
						Contents: "let's pretend this is a service",
						Enable:   true,
						Name:     "service",
					},
				},
			},
		}
		rawIgnCfg, _ := json.Marshal(ignConfig)
		mc := &mcfgv1.MachineConfig{
			TypeMeta: metav1.TypeMeta{
				Kind:       "MachineConfig",
				APIVersion: mcfgapi.GroupName + "/v1",
			},
			ObjectMeta: metav1.ObjectMeta{},
			Spec: mcfgv1.MachineConfigSpec{
				OSImageURL: "",
				Config: runtime.RawExtension{
					Raw: rawIgnCfg,
				},
				KernelArguments: nil,
			},
		}
		unstructuredobj, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(mc)
		obj := &unstructured.Unstructured{Object: unstructuredobj}
		remService = &compv1alpha1.ComplianceRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "remService",
			},
			Spec: compv1alpha1.ComplianceRemediationSpec{
				ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
					Type:  compv1alpha1.McRemediation,
					Apply: false,
				},
				Object: obj,
			},
		}

		remService2 = remService.DeepCopy()

		checkService = &compv1alpha1.ComplianceCheckResult{
			ObjectMeta: metav1.ObjectMeta{
				Name: "checkService",
			},
			ID:          "this_is_some_id",
			Status:      "PASS",
			Description: "This is a dummy check",
		}
		checkService2 = checkService.DeepCopy()

		pRes = &ParseResult{
			CheckResult: checkService,
			Remediation: remService,
		}

		pRes2 = &ParseResult{
			CheckResult: checkService2,
			Remediation: remService2,
		}

		oldList = append(oldList, pRes)
		newList = append(newList, pRes2)
	})

	Context("Same parse results", func() {
		It("passes when the parse results are the same", func() {
			ok := DiffRemediationList(oldList, newList)
			Expect(ok).To(BeTrue())
		})
	})

	Context("Different remediation parse results", func() {
		BeforeEach(func() {
			spec := remService2.Spec.Object.Object["spec"].(map[string]interface{})
			config := spec["config"].(map[string]interface{})
			systemd := config["systemd"].(map[string]interface{})
			units := systemd["units"].([]interface{})
			unitInfo := units[0].(map[string]interface{})
			unitInfo["enable"] = interface{}(false)
		})

		It("fail when the parse results are different", func() {
			ok := DiffRemediationList(oldList, newList)
			Expect(ok).To(BeFalse())
		})
	})

	Context("Different check parse results", func() {
		BeforeEach(func() {
			checkService2.Name = "foo"
		})

		It("fail when the parse results are different", func() {
			ok := DiffRemediationList(oldList, newList)
			Expect(ok).To(BeFalse())
		})
	})

	Context("Different parse results list lengths", func() {
		BeforeEach(func() {
			newList = append(newList, pRes)
		})

		It("fail when the parse results are different", func() {
			ok := DiffRemediationList(oldList, newList)
			Expect(ok).To(BeFalse())
		})
	})

	Context("One or both parse results lists are nil", func() {
		It("fails when one of the lists is nil", func() {
			ok := DiffRemediationList(oldList, nil)
			Expect(ok).To(BeFalse())

			ok = DiffRemediationList(nil, newList)
			Expect(ok).To(BeFalse())
		})

		It("passes when both lists are nil", func() {
			ok := DiffRemediationList(nil, nil)
			Expect(ok).To(BeTrue())
		})
	})

	Context("One list contains remediations, the other does not", func() {
		BeforeEach(func() {
			newList[0].Remediation = nil
		})

		It("fails when one of the remediation lists is nil", func() {
			ok := DiffRemediationList(oldList, newList)
			Expect(ok).To(BeFalse())
		})
	})

	Context("One list contains checks, the other does not", func() {
		BeforeEach(func() {
			newList[0].CheckResult = nil
		})

		It("fails when one of the remediation checks is nil", func() {
			ok := DiffRemediationList(oldList, newList)
			Expect(ok).To(BeFalse())
		})
	})
})
