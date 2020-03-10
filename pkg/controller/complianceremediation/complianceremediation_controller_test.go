package complianceremediation

import (
	"context"
	"fmt"
	igntypes "github.com/coreos/ignition/config/v2_2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
)

func isRemInList(mcList []*mcfgv1.MachineConfig, rem *complianceoperatorv1alpha1.ComplianceRemediation) bool {
	for _, mc := range mcList {
		if same := reflect.DeepEqual(mc.Spec, rem.Spec.MachineConfigContents.Spec); same == true {
			return true
		}
	}

	return false
}

func getMockedRemediation(name string, labels map[string]string, applied bool, status complianceoperatorv1alpha1.RemediationApplicationState) *complianceoperatorv1alpha1.ComplianceRemediation {
	files := []igntypes.File{
		{
			Node: igntypes.Node{
				Path: "/" + name,
			},
		},
	}

	return &complianceoperatorv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: complianceoperatorv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: complianceoperatorv1alpha1.ComplianceRemediationSpecMeta{
				Type:  complianceoperatorv1alpha1.McRemediation,
				Apply: applied,
			},
			MachineConfigContents: mcfgv1.MachineConfig{
				TypeMeta:   metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{},
				Spec: mcfgv1.MachineConfigSpec{
					Config: igntypes.Config{
						Ignition: igntypes.Ignition{
							Version: igntypes.MaxVersion.String(),
						},
						Storage: igntypes.Storage{
							Files: files,
						},
					},
				},
			},
		},
		Status: complianceoperatorv1alpha1.ComplianceRemediationStatus{
			ApplicationState: status,
		},
	}
}

var _ = Describe("Testing complianceremediation controller", func() {

	var (
		complianceremediationinstance *complianceoperatorv1alpha1.ComplianceRemediation
		reconciler                    ReconcileComplianceRemediation
		testRemLabels                 map[string]string
	)

	BeforeEach(func() {
		objs := []runtime.Object{}

		testRemLabels = make(map[string]string)
		testRemLabels[complianceoperatorv1alpha1.SuiteLabel] = "mySuite"
		testRemLabels[complianceoperatorv1alpha1.ScanLabel] = "myScan"
		testRemLabels[mcfgv1.MachineConfigRoleLabelKey] = "myRole"

		// test instance
		complianceremediationinstance = &complianceoperatorv1alpha1.ComplianceRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "testRem",
				Labels: testRemLabels,
			},
		}
		objs = append(objs, complianceremediationinstance)

		scheme := scheme.Scheme
		scheme.AddKnownTypes(complianceoperatorv1alpha1.SchemeGroupVersion, complianceremediationinstance)
		scheme.AddKnownTypes(complianceoperatorv1alpha1.SchemeGroupVersion, &complianceoperatorv1alpha1.ComplianceRemediationList{})

		client := fake.NewFakeClientWithScheme(scheme, objs...)
		reconciler = ReconcileComplianceRemediation{client: client, scheme: scheme}
	})

	Context("Only a single remediation", func() {
		It("should return an empty list if nothing matches", func() {
			machineConfigs, err := getAppliedMcRemediations(&reconciler, complianceremediationinstance)
			Expect(len(machineConfigs)).To(BeZero())
			Expect(err).To(BeNil())
		})
	})

	Context("Multiple matching remediations", func() {
		existingRemediations := make([]*complianceoperatorv1alpha1.ComplianceRemediation, 0)
		const numExisting = 10

		BeforeEach(func() {
			fmt.Println("creating")
			for i := 0; i < numExisting; i++ {
				name := fmt.Sprintf("existingRemediation-%02d", i)
				rem := getMockedRemediation(name, testRemLabels, true, complianceoperatorv1alpha1.RemediationApplied)
				err := reconciler.client.Create(context.TODO(), rem)
				Expect(err).To(BeNil())
				existingRemediations = append(existingRemediations, rem)
			}
		})

		AfterEach(func() {
			for i := 0; i < numExisting; i++ {
				name := fmt.Sprintf("existingRemediation-%02d", i)

				toDelete := complianceoperatorv1alpha1.ComplianceRemediation{}
				err := reconciler.client.Get(context.TODO(), types.NamespacedName{Name: name}, &toDelete)
				Expect(err).To(BeNil())

				err = reconciler.client.Delete(context.TODO(), &toDelete)
				Expect(err).To(BeNil())
			}
		})

		It("should find them all them all", func() {
			machineConfigs, err := getAppliedMcRemediations(&reconciler, complianceremediationinstance)
			Expect(len(machineConfigs)).To(Equal(numExisting))
			Expect(err).To(BeNil())

			for _, rem := range existingRemediations {
				ok := isRemInList(machineConfigs, rem)
				Expect(ok).To(BeTrue())
			}
		})

		It("should skip those that are not applied", func() {
			notApplied := existingRemediations[1]
			notApplied.Status.ApplicationState = complianceoperatorv1alpha1.RemediationNotSelected
			err := reconciler.client.Update(context.TODO(), notApplied)
			Expect(err).To(BeNil())

			machineConfigs, err := getAppliedMcRemediations(&reconciler, complianceremediationinstance)
			Expect(len(machineConfigs)).To(Equal(numExisting - 1))
			Expect(err).To(BeNil())

			for _, rem := range existingRemediations {
				ok := isRemInList(machineConfigs, rem)
				if rem.Name == notApplied.Name {
					Expect(ok).To(BeFalse())
				} else {
					Expect(ok).To(BeTrue())
				}
			}
		})
	})
})
