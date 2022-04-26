package utils_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/compliance-operator/pkg/utils"
)

var _ = Describe("Nodeutils", func() {
	When("Testing IsMcfgPoolUsingKC", func() {
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
			It("Gets correct custom KC name", func() {
				isUsingKC, kc, err := utils.IsMcfgPoolUsingKC(mcp)
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
			It("Get correct custom KC name", func() {
				isUsingKC, kc, err := utils.IsMcfgPoolUsingKC(mcp)
				Expect(err).To(BeNil())
				Expect(isUsingKC).To(BeFalse())
				Expect(kc).To(BeEmpty())

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
							{
								APIVersion: "machineconfiguration.openshift.io/v1",
								Kind:       "MachineConfig",
								Name:       "98-worker-generated-kubelet",
							},
						},
					},
				},
			}
			It("Get correct custom KC name", func() {
				isUsingKC, kc, err := utils.IsMcfgPoolUsingKC(mcp)
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
			It("Get correct custom KC name", func() {
				isUsingKC, kc, err := utils.IsMcfgPoolUsingKC(mcp)
				Expect(err).To(BeNil())
				Expect(isUsingKC).To(BeTrue())
				Expect(kc).To(Equal(expKC))

			})

		})
	})

	When("Testing getting labels from roles", func() {
		DescribeTable("Gets expected output",
			func(role string, expectation map[string]string) {
				got := utils.GetNodeRoleSelector(role)
				Expect(got).To(HaveLen(len(expectation)),
					"node selector wasn't of expected length")
				for expectKey, expectVal := range expectation {
					gotVal, ok := got[expectKey]
					Expect(ok).To(BeTrue(),
						"node selector didn't have expected key '%s'", expectKey)
					Expect(gotVal).To(Equal(expectVal),
						"node selector didn't have expected value '%s'", expectVal)
				}
			},
			Entry("master", "master", map[string]string{"node-role.kubernetes.io/master": ""}),
			Entry("worker", "worker", map[string]string{"node-role.kubernetes.io/worker": ""}),
			Entry("@all", "@all", map[string]string{}),
		)
	})

	Context("MachineConfig Pool with no node selector", func() {
		targetNodeSelector := map[string]string{
			"test-node-role": "",
		}

		mcp := &mcfgv1.MachineConfigPool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "MachineConfigPool",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "invalidpool",
			},
			Spec: mcfgv1.MachineConfigPoolSpec{
				Paused: false,
			},
		}
		It("It should evaluate as false", func() {
			isMatchingPool := utils.McfgPoolLabelMatches(targetNodeSelector, mcp)
			Expect(isMatchingPool).To(BeFalse())

		})

		// no matching labels
		mcp = &mcfgv1.MachineConfigPool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "MachineConfigPool",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "invalidpool",
			},
			Spec: mcfgv1.MachineConfigPoolSpec{
				NodeSelector: &metav1.LabelSelector{},
				Paused:       false,
			},
		}
		It("It should evaluate as false", func() {
			isMatchingPool := utils.McfgPoolLabelMatches(targetNodeSelector, mcp)
			Expect(isMatchingPool).To(BeFalse())

		})

	})
})
