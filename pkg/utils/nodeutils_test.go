package utils_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/ComplianceAsCode/compliance-operator/pkg/utils"
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

	When("Testing IsKCSubsetOfMC", func() {
		defaultKCPayload := `
		{
			"streamingConnectionIdleTimeout": "0s",
			"something": "0s"
		}
		`
		testKubeletConfig := func(kcPayload string) *mcfgv1.KubeletConfig {
			return &mcfgv1.KubeletConfig{
				TypeMeta: metav1.TypeMeta{
					Kind:       "KubeletConfig",
					APIVersion: "machineconfiguration.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "kubelet-config-compliance-operator",
				},
				Spec: mcfgv1.KubeletConfigSpec{
					KubeletConfig: &runtime.RawExtension{
						Raw: []byte(kcPayload),
					},
				},
			}
		}

		testMachineConfig := func(renderdKC string) *mcfgv1.MachineConfig {
			return &mcfgv1.MachineConfig{
				TypeMeta: metav1.TypeMeta{
					Kind:       "MachineConfig",
					APIVersion: "machineconfiguration.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "99-master-generated-kubelet",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "machineconfiguration.openshift.io/v1",
							Kind:       "KubeletConfig",
							Name:       "kubelet-config-compliance-operator",
							UID:        "12345",
						},
					},
				},
				Spec: mcfgv1.MachineConfigSpec{
					Config: runtime.RawExtension{
						Raw: []byte(renderdKC),
					},
				},
			}
		}

		Context("KubeletConfig is a subset of MachineConfig", func() {
			renderdKC := `
			{
				"ignition": {
					"version": "3.2.0"
				},
				"storage": {
					"files": [
						{
							"contents": {
								"source": "data:text/plain,%7B%0A%20%20%22kind%22%3A%20%22KubeletConfiguration%22%2C%0A%20%20%22apiVersion%22%3A%20%22kubelet.config.k8s.io%2Fv1beta1%22%2C%0A%20%20%22staticPodPath%22%3A%20%22%2Fetc%2Fkubernetes%2Fmanifests%22%2C%0A%20%20%22syncFrequency%22%3A%20%220s%22%2C%0A%20%20%22fileCheckFrequency%22%3A%20%220s%22%2C%0A%20%20%22httpCheckFrequency%22%3A%20%220s%22%2C%0A%20%20%22tlsCipherSuites%22%3A%20%5B%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384%22%2C%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256%22%0A%20%20%5D%2C%0A%20%20%22tlsMinVersion%22%3A%20%22VersionTLS12%22%2C%0A%20%20%22rotateCertificates%22%3A%20true%2C%0A%20%20%22serverTLSBootstrap%22%3A%20true%2C%0A%20%20%22authentication%22%3A%20%7B%0A%20%20%20%20%22x509%22%3A%20%7B%0A%20%20%20%20%20%20%22clientCAFile%22%3A%20%22%2Fetc%2Fkubernetes%2Fkubelet-ca.crt%22%0A%20%20%20%20%7D%2C%0A%20%20%20%20%22webhook%22%3A%20%7B%0A%20%20%20%20%20%20%22cacheTTL%22%3A%20%220s%22%0A%20%20%20%20%7D%2C%0A%20%20%20%20%22anonymous%22%3A%20%7B%0A%20%20%20%20%20%20%22enabled%22%3A%20false%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22authorization%22%3A%20%7B%0A%20%20%20%20%22webhook%22%3A%20%7B%0A%20%20%20%20%20%20%22cacheAuthorizedTTL%22%3A%20%220s%22%2C%0A%20%20%20%20%20%20%22cacheUnauthorizedTTL%22%3A%20%220s%22%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22clusterDomain%22%3A%20%22cluster.local%22%2C%0A%20%20%22clusterDNS%22%3A%20%5B%0A%20%20%20%20%22172.30.0.10%22%0A%20%20%5D%2C%0A%20%20%22streamingConnectionIdleTimeout%22%3A%20%220s%22%2C%0A%20%20%22nodeStatusUpdateFrequency%22%3A%20%220s%22%2C%0A%20%20%22nodeStatusReportFrequency%22%3A%20%220s%22%2C%0A%20%20%22imageMinimumGCAge%22%3A%20%220s%22%2C%0A%20%20%22volumeStatsAggPeriod%22%3A%20%220s%22%2C%0A%20%20%22systemCgroups%22%3A%20%22%2Fsystem.slice%22%2C%0A%20%20%22cgroupRoot%22%3A%20%22%2F%22%2C%0A%20%20%22cgroupDriver%22%3A%20%22systemd%22%2C%0A%20%20%22cpuManagerReconcilePeriod%22%3A%20%220s%22%2C%0A%20%20%22runtimeRequestTimeout%22%3A%20%220s%22%2C%0A%20%20%22maxPods%22%3A%20250%2C%0A%20%20%22something%22%3A%20%220s%22%2C%0A%20%20%22kubeAPIBurst%22%3A%20100%2C%0A%20%20%22serializeImagePulls%22%3A%20false%2C%0A%20%20%22evictionPressureTransitionPeriod%22%3A%20%220s%22%2C%0A%20%20%22featureGates%22%3A%20%7B%0A%20%20%20%20%22APIPriorityAndFairness%22%3A%20true%2C%0A%20%20%20%20%22CSIMigrationAWS%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationAzureDisk%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationAzureFile%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationGCE%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationOpenStack%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationvSphere%22%3A%20false%2C%0A%20%20%20%20%22DownwardAPIHugePages%22%3A%20true%2C%0A%20%20%20%20%22LegacyNodeRoleBehavior%22%3A%20false%2C%0A%20%20%20%20%22NodeDisruptionExclusion%22%3A%20true%2C%0A%20%20%20%20%22PodSecurity%22%3A%20true%2C%0A%20%20%20%20%22RotateKubeletServerCertificate%22%3A%20true%2C%0A%20%20%20%20%22ServiceNodeExclusion%22%3A%20true%2C%0A%20%20%20%20%22SupportPodPidsLimit%22%3A%20true%0A%20%20%7D%2C%0A%20%20%22memorySwap%22%3A%20%7B%7D%2C%0A%20%20%22containerLogMaxSize%22%3A%20%2250Mi%22%2C%0A%20%20%22systemReserved%22%3A%20%7B%0A%20%20%20%20%22ephemeral-storage%22%3A%20%221Gi%22%0A%20%20%7D%2C%0A%20%20%22logging%22%3A%20%7B%0A%20%20%20%20%22flushFrequency%22%3A%200%2C%0A%20%20%20%20%22verbosity%22%3A%200%2C%0A%20%20%20%20%22options%22%3A%20%7B%0A%20%20%20%20%20%20%22json%22%3A%20%7B%0A%20%20%20%20%20%20%20%20%22infoBufferSize%22%3A%20%220%22%0A%20%20%20%20%20%20%7D%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22shutdownGracePeriod%22%3A%20%220s%22%2C%0A%20%20%22shutdownGracePeriodCriticalPods%22%3A%20%220s%22%0A%7D%0A"
							},
							"mode": 420,
							"overwrite": true,
							"path": "/etc/kubernetes/kubelet.conf"
						}
					]
				}
			}`
			kc := testKubeletConfig(defaultKCPayload)
			mc := testMachineConfig(renderdKC)

			It("It should evaluate as true", func() {
				isSubset, err, diffString := utils.IsKCSubsetOfMC(kc, mc)
				Expect(err).To(BeNil())
				Expect(isSubset).To(BeTrue())
				Expect(diffString).To(BeEmpty())
			})
		})

		Context("KubeletConfig is a subset of MachineConfig with multiple files", func() {
			renderdKC := `
			{
				"ignition": {
					"version": "3.2.0"
				},
				"storage": {
					"files": [
						{
							"contents": {
								"source": "data:text/plain,NODE_SIZING_ENABLED%3Dtrue%0ASYSTEM_RESERVED_MEMORY%3D1Gi%0ASYSTEM_RESERVED_CPU%3D500m%0A"
							},
							"mode": 420,
							"overwrite": true,
							"path": "/etc/node-sizing-enabled.env"
						},
						{
							"contents": {
								"source": "data:text/plain,%7B%0A%20%20%22kind%22%3A%20%22KubeletConfiguration%22%2C%0A%20%20%22apiVersion%22%3A%20%22kubelet.config.k8s.io%2Fv1beta1%22%2C%0A%20%20%22staticPodPath%22%3A%20%22%2Fetc%2Fkubernetes%2Fmanifests%22%2C%0A%20%20%22syncFrequency%22%3A%20%220s%22%2C%0A%20%20%22fileCheckFrequency%22%3A%20%220s%22%2C%0A%20%20%22httpCheckFrequency%22%3A%20%220s%22%2C%0A%20%20%22tlsCipherSuites%22%3A%20%5B%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384%22%2C%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256%22%0A%20%20%5D%2C%0A%20%20%22tlsMinVersion%22%3A%20%22VersionTLS12%22%2C%0A%20%20%22rotateCertificates%22%3A%20true%2C%0A%20%20%22serverTLSBootstrap%22%3A%20true%2C%0A%20%20%22authentication%22%3A%20%7B%0A%20%20%20%20%22x509%22%3A%20%7B%0A%20%20%20%20%20%20%22clientCAFile%22%3A%20%22%2Fetc%2Fkubernetes%2Fkubelet-ca.crt%22%0A%20%20%20%20%7D%2C%0A%20%20%20%20%22webhook%22%3A%20%7B%0A%20%20%20%20%20%20%22cacheTTL%22%3A%20%220s%22%0A%20%20%20%20%7D%2C%0A%20%20%20%20%22anonymous%22%3A%20%7B%0A%20%20%20%20%20%20%22enabled%22%3A%20false%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22authorization%22%3A%20%7B%0A%20%20%20%20%22webhook%22%3A%20%7B%0A%20%20%20%20%20%20%22cacheAuthorizedTTL%22%3A%20%220s%22%2C%0A%20%20%20%20%20%20%22cacheUnauthorizedTTL%22%3A%20%220s%22%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22clusterDomain%22%3A%20%22cluster.local%22%2C%0A%20%20%22clusterDNS%22%3A%20%5B%0A%20%20%20%20%22172.30.0.10%22%0A%20%20%5D%2C%0A%20%20%22streamingConnectionIdleTimeout%22%3A%20%220s%22%2C%0A%20%20%22nodeStatusUpdateFrequency%22%3A%20%220s%22%2C%0A%20%20%22nodeStatusReportFrequency%22%3A%20%220s%22%2C%0A%20%20%22imageMinimumGCAge%22%3A%20%220s%22%2C%0A%20%20%22volumeStatsAggPeriod%22%3A%20%220s%22%2C%0A%20%20%22systemCgroups%22%3A%20%22%2Fsystem.slice%22%2C%0A%20%20%22cgroupRoot%22%3A%20%22%2F%22%2C%0A%20%20%22cgroupDriver%22%3A%20%22systemd%22%2C%0A%20%20%22cpuManagerReconcilePeriod%22%3A%20%220s%22%2C%0A%20%20%22runtimeRequestTimeout%22%3A%20%220s%22%2C%0A%20%20%22maxPods%22%3A%20250%2C%0A%20%20%22something%22%3A%20%220s%22%2C%0A%20%20%22kubeAPIBurst%22%3A%20100%2C%0A%20%20%22serializeImagePulls%22%3A%20false%2C%0A%20%20%22evictionPressureTransitionPeriod%22%3A%20%220s%22%2C%0A%20%20%22featureGates%22%3A%20%7B%0A%20%20%20%20%22APIPriorityAndFairness%22%3A%20true%2C%0A%20%20%20%20%22CSIMigrationAWS%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationAzureDisk%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationAzureFile%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationGCE%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationOpenStack%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationvSphere%22%3A%20false%2C%0A%20%20%20%20%22DownwardAPIHugePages%22%3A%20true%2C%0A%20%20%20%20%22LegacyNodeRoleBehavior%22%3A%20false%2C%0A%20%20%20%20%22NodeDisruptionExclusion%22%3A%20true%2C%0A%20%20%20%20%22PodSecurity%22%3A%20true%2C%0A%20%20%20%20%22RotateKubeletServerCertificate%22%3A%20true%2C%0A%20%20%20%20%22ServiceNodeExclusion%22%3A%20true%2C%0A%20%20%20%20%22SupportPodPidsLimit%22%3A%20true%0A%20%20%7D%2C%0A%20%20%22memorySwap%22%3A%20%7B%7D%2C%0A%20%20%22containerLogMaxSize%22%3A%20%2250Mi%22%2C%0A%20%20%22systemReserved%22%3A%20%7B%0A%20%20%20%20%22ephemeral-storage%22%3A%20%221Gi%22%0A%20%20%7D%2C%0A%20%20%22logging%22%3A%20%7B%0A%20%20%20%20%22flushFrequency%22%3A%200%2C%0A%20%20%20%20%22verbosity%22%3A%200%2C%0A%20%20%20%20%22options%22%3A%20%7B%0A%20%20%20%20%20%20%22json%22%3A%20%7B%0A%20%20%20%20%20%20%20%20%22infoBufferSize%22%3A%20%220%22%0A%20%20%20%20%20%20%7D%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22shutdownGracePeriod%22%3A%20%220s%22%2C%0A%20%20%22shutdownGracePeriodCriticalPods%22%3A%20%220s%22%0A%7D%0A"
							},
							"mode": 420,
							"overwrite": true,
							"path": "/etc/kubernetes/kubelet.conf"
						}
					]
				}
			}`
			kc := testKubeletConfig(defaultKCPayload)
			mc := testMachineConfig(renderdKC)

			It("It should evaluate as true", func() {
				isSubset, err, diffString := utils.IsKCSubsetOfMC(kc, mc)
				Expect(err).To(BeNil())
				Expect(isSubset).To(BeTrue())
				Expect(diffString).To(BeEmpty())
			})
		})

		Context("KubeletConfig is not a subset of MachineConfig", func() {
			renderdKC := `
			{
				"ignition": {
					"version": "3.2.0"
				},
				"storage": {
					"files": [
						{
							"contents": {
								"source": "data:text/plain,%7B%0A%20%20%22kind%22%3A%20%22KubeletConfiguration%22%2C%0A%20%20%22apiVersion%22%3A%20%22kubelet.config.k8s.io%2Fv1beta1%22%2C%0A%20%20%22staticPodPath%22%3A%20%22%2Fetc%2Fkubernetes%2Fmanifests%22%2C%0A%20%20%22syncFrequency%22%3A%20%220s%22%2C%0A%20%20%22fileCheckFrequency%22%3A%20%220s%22%2C%0A%20%20%22httpCheckFrequency%22%3A%20%220s%22%2C%0A%20%20%22tlsCipherSuites%22%3A%20%5B%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384%22%2C%0A%20%20%20%20%22TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256%22%2C%0A%20%20%20%20%22TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256%22%0A%20%20%5D%2C%0A%20%20%22tlsMinVersion%22%3A%20%22VersionTLS12%22%2C%0A%20%20%22rotateCertificates%22%3A%20true%2C%0A%20%20%22serverTLSBootstrap%22%3A%20true%2C%0A%20%20%22authentication%22%3A%20%7B%0A%20%20%20%20%22x509%22%3A%20%7B%0A%20%20%20%20%20%20%22clientCAFile%22%3A%20%22%2Fetc%2Fkubernetes%2Fkubelet-ca.crt%22%0A%20%20%20%20%7D%2C%0A%20%20%20%20%22webhook%22%3A%20%7B%0A%20%20%20%20%20%20%22cacheTTL%22%3A%20%220s%22%0A%20%20%20%20%7D%2C%0A%20%20%20%20%22anonymous%22%3A%20%7B%0A%20%20%20%20%20%20%22enabled%22%3A%20false%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22authorization%22%3A%20%7B%0A%20%20%20%20%22webhook%22%3A%20%7B%0A%20%20%20%20%20%20%22cacheAuthorizedTTL%22%3A%20%220s%22%2C%0A%20%20%20%20%20%20%22cacheUnauthorizedTTL%22%3A%20%220s%22%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22clusterDomain%22%3A%20%22cluster.local%22%2C%0A%20%20%22clusterDNS%22%3A%20%5B%0A%20%20%20%20%22172.30.0.10%22%0A%20%20%5D%2C%0A%20%20%22streamingConnectionIdleTimeout%22%3A%20%220s%22%2C%0A%20%20%22nodeStatusUpdateFrequency%22%3A%20%220s%22%2C%0A%20%20%22nodeStatusReportFrequency%22%3A%20%220s%22%2C%0A%20%20%22imageMinimumGCAge%22%3A%20%220s%22%2C%0A%20%20%22volumeStatsAggPeriod%22%3A%20%220s%22%2C%0A%20%20%22systemCgroups%22%3A%20%22%2Fsystem.slice%22%2C%0A%20%20%22cgroupRoot%22%3A%20%22%2F%22%2C%0A%20%20%22cgroupDriver%22%3A%20%22systemd%22%2C%0A%20%20%22cpuManagerReconcilePeriod%22%3A%20%220s%22%2C%0A%20%20%22runtimeRequestTimeout%22%3A%20%220s%22%2C%0A%20%20%22maxPods%22%3A%20250%2C%0A%20%20%22kubeAPIQPS%22%3A%2050%2C%0A%20%20%22kubeAPIBurst%22%3A%20100%2C%0A%20%20%22serializeImagePulls%22%3A%20false%2C%0A%20%20%22evictionPressureTransitionPeriod%22%3A%20%220s%22%2C%0A%20%20%22featureGates%22%3A%20%7B%0A%20%20%20%20%22APIPriorityAndFairness%22%3A%20true%2C%0A%20%20%20%20%22CSIMigrationAWS%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationAzureDisk%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationAzureFile%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationGCE%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationOpenStack%22%3A%20false%2C%0A%20%20%20%20%22CSIMigrationvSphere%22%3A%20false%2C%0A%20%20%20%20%22DownwardAPIHugePages%22%3A%20true%2C%0A%20%20%20%20%22LegacyNodeRoleBehavior%22%3A%20false%2C%0A%20%20%20%20%22NodeDisruptionExclusion%22%3A%20true%2C%0A%20%20%20%20%22PodSecurity%22%3A%20true%2C%0A%20%20%20%20%22RotateKubeletServerCertificate%22%3A%20true%2C%0A%20%20%20%20%22ServiceNodeExclusion%22%3A%20true%2C%0A%20%20%20%20%22SupportPodPidsLimit%22%3A%20true%0A%20%20%7D%2C%0A%20%20%22memorySwap%22%3A%20%7B%7D%2C%0A%20%20%22containerLogMaxSize%22%3A%20%2250Mi%22%2C%0A%20%20%22systemReserved%22%3A%20%7B%0A%20%20%20%20%22ephemeral-storage%22%3A%20%221Gi%22%0A%20%20%7D%2C%0A%20%20%22logging%22%3A%20%7B%0A%20%20%20%20%22flushFrequency%22%3A%200%2C%0A%20%20%20%20%22verbosity%22%3A%200%2C%0A%20%20%20%20%22options%22%3A%20%7B%0A%20%20%20%20%20%20%22json%22%3A%20%7B%0A%20%20%20%20%20%20%20%20%22infoBufferSize%22%3A%20%220%22%0A%20%20%20%20%20%20%7D%0A%20%20%20%20%7D%0A%20%20%7D%2C%0A%20%20%22shutdownGracePeriod%22%3A%20%220s%22%2C%0A%20%20%22shutdownGracePeriodCriticalPods%22%3A%20%220s%22%0A%7D%0A"
							},
							"mode": 420,
							"overwrite": true,
							"path": "/etc/kubernetes/kubelet.conf"
						}
					]
				}
			}`
			kc := testKubeletConfig(defaultKCPayload)
			mc := testMachineConfig(renderdKC)
			expectedDiffString := "kubeletconfig kubelet-config-compliance-operator is not subset of rendered MC 99-master-generated-kubelet, diff: [[Path: /something Expected: 0s Got: NOT FOUND]]"

			It("It should evaluate as false", func() {
				isSubset, err, diffString := utils.IsKCSubsetOfMC(kc, mc)
				Expect(err).To(BeNil())
				Expect(isSubset).To(BeFalse())
				Expect(diffString).To(Equal(expectedDiffString))
			})
		})

		Context("MachineConfig is missing kubeconfig file", func() {
			renderdKC := `
			{
				"ignition": {
					"version": "3.2.0"
				},
				"storage": {
					"files": []
				}
			}`
			kc := testKubeletConfig(defaultKCPayload)
			mc := testMachineConfig(renderdKC)
			expectedError := "encoded kubeletconfig 99-master-generated-kubelet is missing"

			It("It should evaluate as false", func() {
				isSubset, err, diffString := utils.IsKCSubsetOfMC(kc, mc)
				Expect(err).To(MatchError(expectedError))
				Expect(isSubset).To(BeFalse())
				Expect(diffString).To(BeEmpty())
			})
		})

		Context("MachineConfig kubeconfig is empty", func() {
			renderdKC := `
			{
				"ignition": {
					"version": "3.2.0"
				},
				"storage": {
					"files": [
						{
							"contents": {
								"source": ""
							},
							"mode": 420,
							"overwrite": true,
							"path": "/etc/kubernetes/kubelet.conf"
						}
					]
				}
			}`
			kc := testKubeletConfig(defaultKCPayload)
			mc := testMachineConfig(renderdKC)
			expectedError := "encoded kubeletconfig 99-master-generated-kubelet is empty"

			It("It should evaluate as false", func() {
				isSubset, err, diffString := utils.IsKCSubsetOfMC(kc, mc)
				Expect(err).To(MatchError(expectedError))
				Expect(isSubset).To(BeFalse())
				Expect(diffString).To(BeEmpty())
			})
		})

		Context("KubeletConfig is nil", func() {
			var kc *mcfgv1.KubeletConfig
			mc := &mcfgv1.MachineConfig{}
			expectedError := "kubelet config is nil"

			It("It should evaluate as false", func() {
				isSubset, err, diffString := utils.IsKCSubsetOfMC(kc, mc)
				Expect(err).To(MatchError(expectedError))
				Expect(isSubset).To(BeFalse())
				Expect(diffString).To(BeEmpty())
			})
		})

		Context("MachineConfig is nil", func() {
			kc := &mcfgv1.KubeletConfig{}
			var mc *mcfgv1.MachineConfig
			expectedError := "machine config is nil"

			It("It should evaluate as false", func() {
				isSubset, err, diffString := utils.IsKCSubsetOfMC(kc, mc)
				Expect(err).To(MatchError(expectedError))
				Expect(isSubset).To(BeFalse())
				Expect(diffString).To(BeEmpty())
			})
		})

		Context("KubeletConfig and MachineConfig are empty", func() {
			kc := &mcfgv1.KubeletConfig{}
			mc := &mcfgv1.MachineConfig{}
			expectedError := "failed to unmarshal machine config : unexpected end of JSON input"

			It("It should evaluate as false", func() {
				isSubset, err, diffString := utils.IsKCSubsetOfMC(kc, mc)
				Expect(err).To(MatchError(expectedError))
				Expect(isSubset).To(BeFalse())
				Expect(diffString).To(BeEmpty())
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
