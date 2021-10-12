package utils_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/compliance-operator/pkg/utils"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Nodeutils", func() {
	Context("MachineConfig Pool with one custom KubeletConfig", func() {
		targetNodeSelector := map[string]string{
			"test-node-role": "",
		}
		const expKC string = "99-worker-generated-kubelet"

		mcp := &mcfgv1.MachineConfigPool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "MachineConfigPool",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "testPool Name",
			},
			Spec: mcfgv1.MachineConfigPoolSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: targetNodeSelector,
				},
				Configuration: mcfgv1.MachineConfigPoolStatusConfiguration{
					Source: []corev1.ObjectReference{
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "01-worker-kubelet",
						},
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "99-worker-generated-kubelet",
						},
					},
				},
			},
		}
		isUsingKC, kc, err := utils.IsMcfgPoolUsingKC(mcp)
		It("Get correct custom KC name", func() {
			Expect(err).To(BeNil())
			Expect(isUsingKC).To(BeTrue())
			Expect(kc).To(Equal(expKC))

		})

	})

	Context("MachineConfig Pool with no custom KubeletConfig", func() {
		targetNodeSelector := map[string]string{
			"test-node-role": "",
		}

		mcp := &mcfgv1.MachineConfigPool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "MachineConfigPool",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "testPool Name",
			},
			Spec: mcfgv1.MachineConfigPoolSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: targetNodeSelector,
				},
				Configuration: mcfgv1.MachineConfigPoolStatusConfiguration{
					Source: []corev1.ObjectReference{
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "01-worker-kubelet",
						},
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "50-workers-chrony-configuration",
						},
					},
				},
			},
		}
		isUsingKC, kc, err := utils.IsMcfgPoolUsingKC(mcp)
		It("Get correct custom KC name", func() {
			Expect(err).To(BeNil())
			Expect(isUsingKC).To(BeFalse())
			Expect(kc).To(BeEmpty())

		})

	})

	Context("MachineConfig Pool with many custom KubeletConfig", func() {
		targetNodeSelector := map[string]string{
			"test-node-role": "",
		}
		const expKC string = "99-worker-generated-kubelet-3"

		mcp := &mcfgv1.MachineConfigPool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "MachineConfigPool",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "testPool Name",
			},
			Spec: mcfgv1.MachineConfigPoolSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: targetNodeSelector,
				},
				Configuration: mcfgv1.MachineConfigPoolStatusConfiguration{
					Source: []corev1.ObjectReference{
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "01-worker-kubelet",
						},
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "99-worker-generated-kubelet",
						},
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "99-worker-generated-kubelet-1",
						},
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "99-worker-generated-kubelet-2",
						},
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "MachineConfig",
							Name:       "99-worker-generated-kubelet-3",
						},
					},
				},
			},
		}
		isUsingKC, kc, err := utils.IsMcfgPoolUsingKC(mcp)
		It("Get correct custom KC name", func() {
			Expect(err).To(BeNil())
			Expect(isUsingKC).To(BeTrue())
			Expect(kc).To(Equal(expKC))

		})

	})

})
