package complianceremediation

import (
	"context"
	"fmt"
	"reflect"

	"github.com/clarketm/json"
	igntypes "github.com/coreos/ignition/config/v2_2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/utils"
)

func isRemInList(mcList []*mcfgv1.MachineConfig, rem *compv1alpha1.ComplianceRemediation) bool {
	remMc, _ := utils.ParseMachineConfig(rem, rem.Spec.Object)
	for _, mc := range mcList {
		if same := reflect.DeepEqual(mc.Spec, remMc.Spec); same == true {
			return true
		}
	}

	return false
}

func getMockedRemediation(name string, labels map[string]string, applied bool, status compv1alpha1.RemediationApplicationState) *compv1alpha1.ComplianceRemediation {
	files := []igntypes.File{
		{
			Node: igntypes.Node{
				Path: "/" + name,
			},
		},
	}

	ign := igntypes.Config{
		Storage: igntypes.Storage{
			Files: files,
		},
	}
	rawIgn, _ := json.Marshal(ign)
	mc := &mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MachineConfig",
			APIVersion: mcfgapi.GroupName + "/v1",
		},
		ObjectMeta: metav1.ObjectMeta{},
		Spec: mcfgv1.MachineConfigSpec{
			Config: runtime.RawExtension{
				Raw: rawIgn,
			},
		},
	}
	unstructuredobj, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(mc)
	obj := &unstructured.Unstructured{Object: unstructuredobj}
	return &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: applied,
			},
			Object: obj,
		},
		Status: compv1alpha1.ComplianceRemediationStatus{
			ApplicationState: status,
		},
	}
}

var _ = Describe("Testing complianceremediation controller", func() {

	var (
		complianceremediationinstance *compv1alpha1.ComplianceRemediation
		reconciler                    ReconcileComplianceRemediation
		testRemLabels                 map[string]string
	)

	BeforeEach(func() {
		objs := []runtime.Object{}

		testRemLabels = make(map[string]string)
		testRemLabels[compv1alpha1.SuiteLabel] = "mySuite"
		testRemLabels[compv1alpha1.ScanLabel] = "myScan"
		testRemLabels[mcfgv1.MachineConfigRoleLabelKey] = "myRole"

		// test instance
		complianceremediationinstance = &compv1alpha1.ComplianceRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "testRem",
				Labels: testRemLabels,
			},
			Spec: compv1alpha1.ComplianceRemediationSpec{
				Object: nil,
			},
		}
		objs = append(objs, complianceremediationinstance)

		scheme := scheme.Scheme
		scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion, complianceremediationinstance)
		scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion, &compv1alpha1.ComplianceRemediationList{})

		client := fake.NewFakeClientWithScheme(scheme, objs...)
		reconciler = ReconcileComplianceRemediation{client: client, scheme: scheme}
	})

	Context("only a single remediation", func() {
		It("should return an empty list if nothing matches", func() {
			machineconfigs, err := getAppliedMcRemediations(&reconciler, complianceremediationinstance)
			Expect(len(machineconfigs)).To(BeZero())
			Expect(err).To(BeNil())
		})
	})

	Context("Multiple matching remediations", func() {
		existingRemediations := make([]*compv1alpha1.ComplianceRemediation, 0)
		const numExisting = 10

		BeforeEach(func() {
			for i := 0; i < numExisting; i++ {
				name := fmt.Sprintf("existingRemediation-%02d", i)
				rem := getMockedRemediation(name, testRemLabels, true, compv1alpha1.RemediationApplied)
				err := reconciler.client.Create(context.TODO(), rem)
				Expect(err).To(BeNil())
				existingRemediations = append(existingRemediations, rem)
			}
		})

		AfterEach(func() {
			for i := 0; i < numExisting; i++ {
				name := fmt.Sprintf("existingRemediation-%02d", i)

				toDelete := compv1alpha1.ComplianceRemediation{}
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
			notApplied.Status.ApplicationState = compv1alpha1.RemediationNotApplied
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

	Context("getting remediation name annotations", func() {
		It("should get non-cropped remediation name if it's short enough", func() {
			annotationKey := getRemediationAnnotationKey("simple-remediation")
			Expect(len(annotationKey)).ToNot(BeZero())
			Expect(len(annotationKey)).To(BeNumerically("<", 64))
		})

		It("should get cropped remediation name if it's too long", func() {
			annotationKey := getRemediationAnnotationKey(
				"moderate-master-scan-1-sysctl-net-ipv4-icmp-echo-ignore-broadcasts123456789" +
					"abcdefghijklmnopqrstuvwxyz")
			Expect(len(annotationKey)).ToNot(BeZero())
			Expect(len(annotationKey)).To(BeNumerically("<", 64))
		})
	})
})
