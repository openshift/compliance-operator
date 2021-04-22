package utils

import (
	"fmt"

	"github.com/clarketm/json"
	igntypes "github.com/coreos/ignition/v2/config/v3_1/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func getItemById(list []*ParseResultContextItem, id string) *ParseResultContextItem {
	for _, item := range list {
		if id == item.Id {
			return item
		}
	}

	return nil
}

func getRemediation(serviceName string) *compv1alpha1.ComplianceRemediation {
	serviceStr := "let's pretend this is a service"
	trueVal := true
	ignConfig := igntypes.Config{
		Systemd: igntypes.Systemd{
			Units: []igntypes.Unit{
				{
					Contents: &serviceStr,
					Enabled:  &trueVal,
					Name:     serviceName,
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

	return &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "remService",
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: false,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: obj,
			},
		},
	}
}

func checkWithRemediation(id, serviceName string) *ParseResult {
	checkService := &compv1alpha1.ComplianceCheckResult{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkService",
		},
		ID:          id,
		Status:      "PASS",
		Description: "This is a dummy check",
	}

	return &ParseResult{
		Id:          id,
		CheckResult: checkService,
		Remediation: getRemediation(serviceName),
	}
}

var _ = Describe("Testing reconciling differing parse results", func() {
	var (
		list1 []*ParseResult
		list2 []*ParseResult
		list3 []*ParseResult

		consistent []*ParseResultContextItem
		reconciled *ParseResultContextItem
		err        error

		prCtx *ParseResultContext
	)

	AssertReconcilesWithResults := func(results string) {
		It("Reconciles the previously inconsistent result", func() {
			Expect(err).To(BeNil())
			Expect(consistent).To(HaveLen(3))
		})

		It("Marks every source separately in an annotation", func() {
			Expect(reconciled.Annotations).ToNot(BeNil())
			Expect(reconciled.Annotations).To(HaveKeyWithValue(compv1alpha1.ComplianceCheckResultInconsistentSourceAnnotation, results))
			Expect(reconciled.Annotations).ToNot(HaveKey(compv1alpha1.ComplianceCheckResultMostCommonAnnotation))
		})

		It("Does not find a common state", func() {
			Expect(reconciled.Annotations).ToNot(BeNil())
			Expect(reconciled.Annotations).ToNot(HaveKey(compv1alpha1.ComplianceCheckResultMostCommonAnnotation))
		})
	}

	ParseAndReconcile := func() {
		prCtx.AddResults("source1", list1)
		prCtx.AddResults("source2", list2)
		prCtx.AddResults("source3", list3)

		consistent = prCtx.GetConsistentResults()
	}

	BeforeEach(func() {
		list1 = []*ParseResult{}
		list2 = []*ParseResult{}
		list3 = []*ParseResult{}

		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("checkid_%d", i)
			service := fmt.Sprintf("service_%d", i)

			list1 = append(list1, checkWithRemediation(id, service))
			list2 = append(list2, checkWithRemediation(id, service))
			list3 = append(list3, checkWithRemediation(id, service))
		}

		prCtx = NewParseResultContext()
	})

	Context("Handling consistent results", func() {
		It("Merges all results to the consistent list", func() {
			prCtx.AddResults("source1", list1)
			Expect(prCtx.consistent).To(HaveLen(3))
			Expect(prCtx.inconsistent).To(HaveLen(0))
			Expect(prCtx.consistent["checkid_1"].sources).To(ConsistOf("source1"))

			prCtx.AddResults("source2", list2)
			Expect(prCtx.consistent).To(HaveLen(3))
			Expect(prCtx.inconsistent).To(HaveLen(0))
			Expect(prCtx.consistent["checkid_1"].sources).To(ConsistOf("source1", "source2"))

			prCtx.AddResults("source3", list3)
			Expect(prCtx.consistent).To(HaveLen(3))
			Expect(prCtx.inconsistent).To(HaveLen(0))
			Expect(prCtx.consistent["checkid_1"].sources).To(ConsistOf("source1", "source2", "source3"))
		})
	})

	Context("Handling inconsistent results", func() {
		JustBeforeEach(func() {
			list2[0].CheckResult.Status = compv1alpha1.CheckResultFail
		})

		It("Indentifies the inconsistent result", func() {
			prCtx.AddResults("source1", list1)
			Expect(prCtx.consistent).To(HaveLen(3))
			Expect(prCtx.inconsistent).To(HaveLen(0))
			Expect(prCtx.consistent["checkid_1"].sources).To(ConsistOf("source1"))

			prCtx.AddResults("source2", list2)
			Expect(prCtx.consistent).To(HaveLen(2))
			Expect(prCtx.inconsistent).To(HaveLen(1))
			Expect(prCtx.inconsistent["checkid_0"][0].sources).To(ConsistOf("source1"))
			Expect(prCtx.inconsistent["checkid_0"][0].CheckResult.Status).To(BeEquivalentTo(compv1alpha1.CheckResultPass))
			Expect(prCtx.inconsistent["checkid_0"][1].sources).To(ConsistOf("source2"))
			Expect(prCtx.inconsistent["checkid_0"][1].CheckResult.Status).To(BeEquivalentTo(compv1alpha1.CheckResultFail))

			prCtx.AddResults("source3", list3)
			Expect(prCtx.consistent).To(HaveLen(2))
			Expect(prCtx.inconsistent).To(HaveLen(1))
			Expect(prCtx.inconsistent["checkid_0"][0].sources).To(ConsistOf("source1"))
			Expect(prCtx.inconsistent["checkid_0"][0].CheckResult.Status).To(BeEquivalentTo(compv1alpha1.CheckResultPass))
			Expect(prCtx.inconsistent["checkid_0"][1].sources).To(ConsistOf("source2"))
			Expect(prCtx.inconsistent["checkid_0"][1].CheckResult.Status).To(BeEquivalentTo(compv1alpha1.CheckResultFail))
			Expect(prCtx.inconsistent["checkid_0"][2].sources).To(ConsistOf("source3"))
			Expect(prCtx.inconsistent["checkid_0"][2].CheckResult.Status).To(BeEquivalentTo(compv1alpha1.CheckResultPass))
		})
	})

	Context("Reconciling inconsistent results", func() {
		JustBeforeEach(func() {
			list2[0].CheckResult.Status = compv1alpha1.CheckResultFail

			ParseAndReconcile()
			reconciled = getItemById(consistent, "checkid_0")
		})

		It("Reconciles the previously inconsistent result", func() {
			Expect(err).To(BeNil())
			Expect(consistent).To(HaveLen(3))
		})

		It("Annotates the previously inconsistent result", func() {
			Expect(reconciled.Annotations).ToNot(BeNil())
			Expect(reconciled.Annotations).To(HaveKeyWithValue(compv1alpha1.ComplianceCheckResultInconsistentSourceAnnotation, "source2:FAIL"))
			Expect(reconciled.Annotations).To(HaveKeyWithValue(compv1alpha1.ComplianceCheckResultMostCommonAnnotation, "PASS"))
		})

		It("Creates a remediation", func() {
			Expect(reconciled.Remediation).ToNot(BeNil())
		})
	})

	Context("No common result", func() {
		JustBeforeEach(func() {
			list2[0].CheckResult.Status = compv1alpha1.CheckResultFail
			list3[0].CheckResult.Status = compv1alpha1.CheckResultInfo

			ParseAndReconcile()
			reconciled = getItemById(consistent, "checkid_0")
		})

		AssertReconcilesWithResults("source1:PASS,source2:FAIL,source3:INFO")

		It("Creates a remediation", func() {
			Expect(reconciled.Remediation).ToNot(BeNil())
		})
	})

	Context("Result that prevents creating a remediation", func() {
		JustBeforeEach(func() {
			list2[0].CheckResult.Status = compv1alpha1.CheckResultError
			list3[0].CheckResult.Status = compv1alpha1.CheckResultInfo

			ParseAndReconcile()
			reconciled = getItemById(consistent, "checkid_0")
		})

		AssertReconcilesWithResults("source1:PASS,source2:ERROR,source3:INFO")

		It("Does NOT create a remediation because one of the checks errored out", func() {
			Expect(reconciled.Remediation).To(BeNil())
		})
	})

	Context("If the remediations differ, the check result is ERROR", func() {
		JustBeforeEach(func() {
			list2[0].Remediation = getRemediation("anotherService")

			ParseAndReconcile()
			reconciled = getItemById(consistent, "checkid_0")
		})

		It("Marks the check as an error", func() {
			Expect(reconciled.CheckResult.Status).To(BeEquivalentTo("ERROR"))
			Expect(reconciled.Annotations).ToNot(BeNil())
			Expect(reconciled.Annotations).To(HaveKey(compv1alpha1.ComplianceCheckResultErrorAnnotation))
		})

		It("Does NOT create a remediation because one of the checks errored out", func() {
			Expect(reconciled.Remediation).To(BeNil())
		})
	})
})
