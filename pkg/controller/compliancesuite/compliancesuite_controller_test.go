package compliancesuite

import (
	igntypes "github.com/coreos/ignition/config/v2_2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

var _ = Describe("Testing remediations diff", func() {
	var (
		remService  *compv1alpha1.ComplianceRemediation
		remService2 *compv1alpha1.ComplianceRemediation
		oldList     []*compv1alpha1.ComplianceRemediation
		newList     []*compv1alpha1.ComplianceRemediation
	)

	BeforeEach(func() {
		remService = &compv1alpha1.ComplianceRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name: "remService",
			},
			Spec: compv1alpha1.ComplianceRemediationSpec{
				ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
					Type:  compv1alpha1.McRemediation,
					Apply: false,
				},
				MachineConfigContents: mcfgv1.MachineConfig{
					Spec: mcfgv1.MachineConfigSpec{
						OSImageURL: "",
						Config: igntypes.Config{
							Systemd: igntypes.Systemd{
								Units: []igntypes.Unit{
									{
										Contents: "let's pretend this is a service",
										Enable:   true,
										Name:     "service",
									},
								},
							},
						},
						KernelArguments: nil,
					},
				},
			},
		}

		remService2 = remService.DeepCopy()

		oldList = append(newList, remService)
		newList = append(newList, remService2)
	})

	Context("Same remediations", func() {
		It("passes when the remediations are the same", func() {
			ok := diffRemediationList(oldList, newList)
			Expect(ok).To(BeTrue())
		})
	})

	Context("Different remediations", func() {
		BeforeEach(func() {
			remService2.Spec.MachineConfigContents.Spec.Config.Systemd.Units[0].Enable = false
		})

		It("fail when the remediations are different", func() {
			ok := diffRemediationList(oldList, newList)
			Expect(ok).To(BeFalse())
		})
	})

	Context("Different remediation list lengths", func() {
		BeforeEach(func() {
			newList = append(newList, remService)
		})

		It("fail when the remediations are different", func() {
			ok := diffRemediationList(oldList, newList)
			Expect(ok).To(BeFalse())
		})
	})

	Context("One or both remediation lists are nil", func() {
		It("fails when one of the lists is nil", func() {
			ok := diffRemediationList(oldList, nil)
			Expect(ok).To(BeFalse())

			ok = diffRemediationList(nil, newList)
			Expect(ok).To(BeFalse())
		})

		It("passes when both lists are nil", func() {
			ok := diffRemediationList(nil, nil)
			Expect(ok).To(BeTrue())
		})
	})
})
