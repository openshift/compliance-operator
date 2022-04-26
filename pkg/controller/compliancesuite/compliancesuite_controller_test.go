package compliancesuite

import (
	"context"
	"encoding/json"

	"github.com/openshift/compliance-operator/pkg/controller/metrics"
	"github.com/openshift/compliance-operator/pkg/controller/metrics/metricsfakes"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/compliance-operator/pkg/apis"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

var _ = Describe("ComplianceSuiteController", func() {
	var (
		suite              *compv1alpha1.ComplianceSuite
		reconciler         *ReconcileComplianceSuite
		logger             logr.Logger
		ctx                = context.Background()
		namespace          = "test-ns"
		suiteName          = "testSuite"
		remediationName    = "testRem"
		targetNodeSelector = map[string]string{
			"hops": "malt",
		}
	)

	suiteAndScansInDonePhase := func() {
		scan1Key := types.NamespacedName{Name: "testScanNode", Namespace: namespace}

		scan1 := &compv1alpha1.ComplianceScan{}

		err := reconciler.client.Get(ctx, scan1Key, scan1)
		Expect(err).To(BeNil())

		suite.Status.Phase = compv1alpha1.PhaseDone
		scan1Copy := scan1.DeepCopy()
		scan1Copy.Status.Phase = compv1alpha1.PhaseDone

		err = reconciler.client.Status().Update(ctx, scan1Copy)
		Expect(err).To(BeNil())
		err = reconciler.client.Status().Update(ctx, suite)
		Expect(err).To(BeNil())
	}

	BeforeEach(func() {
		nodeScanSettings := compv1alpha1.ComplianceScanSpec{
			ScanType:     compv1alpha1.ScanTypeNode,
			NodeSelector: targetNodeSelector,
		}

		suite = &compv1alpha1.ComplianceSuite{
			ObjectMeta: metav1.ObjectMeta{
				Name:      suiteName,
				Namespace: namespace,
			},
			Spec: compv1alpha1.ComplianceSuiteSpec{
				Scans: []compv1alpha1.ComplianceScanSpecWrapper{
					{
						Name:               "testScanNode",
						ComplianceScanSpec: nodeScanSettings,
					},
				},
			},
		}
		nodeScan := &compv1alpha1.ComplianceScan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testScanNode",
				Namespace: namespace,
			},
			Spec: nodeScanSettings,
		}

		cscheme := scheme.Scheme
		err := apis.AddToScheme(cscheme)
		Expect(err).To(BeNil())
		err = mcfgapi.Install(cscheme)
		Expect(err).To(BeNil())

		client := fake.NewFakeClientWithScheme(cscheme, nodeScan.DeepCopy(), suite.DeepCopy())
		mockMetrics := metrics.NewMetrics(&metricsfakes.FakeImpl{})
		err = mockMetrics.Register()
		Expect(err).To(BeNil())

		reconciler = &ReconcileComplianceSuite{reader: client, client: client, scheme: cscheme, metrics: mockMetrics}
		zaplog, _ := zap.NewDevelopment()
		logger = zapr.NewLogger(zaplog)
	})

	reconcileAndGetRemediation := func() *compv1alpha1.ComplianceRemediation {
		_, err := reconciler.reconcileRemediations(suite, logger)
		Expect(err).To(BeNil())

		rem := &compv1alpha1.ComplianceRemediation{}
		key := types.NamespacedName{Name: remediationName, Namespace: namespace}
		getErr := reconciler.client.Get(ctx, key, rem)
		Expect(getErr).To(BeNil())
		return rem
	}

	reconcileShouldNotApplyTheRemediation := func() {
		rem := reconcileAndGetRemediation()
		Expect(rem.Spec.Apply).To(BeFalse())
	}

	prepareOutdatedRemediation := func() {
		rem := &compv1alpha1.ComplianceRemediation{}
		remKey := types.NamespacedName{Name: remediationName, Namespace: namespace}
		err := reconciler.client.Get(ctx, remKey, rem)
		Expect(err).To(BeNil())
		cm := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: "v1",
			},
		}
		unstructuredCM, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
		Expect(err).ToNot(HaveOccurred())
		remCopy := rem.DeepCopy()
		remCopy.Spec.Current.Object = &unstructured.Unstructured{
			Object: unstructuredCM,
		}
		err = reconciler.client.Update(ctx, remCopy)
		Expect(err).To(BeNil())
		remCopyForStatus := remCopy.DeepCopy()
		remCopyForStatus.Status.ApplicationState = compv1alpha1.RemediationOutdated
		err = reconciler.client.Status().Update(ctx, remCopyForStatus)
		Expect(err).To(BeNil())
	}

	prepareForRemoveOutdatedScenarios := func() {
		prepareOutdatedRemediation()
		if suite.Annotations == nil {
			suite.Annotations = make(map[string]string, 1)
		}
		suite.Annotations[compv1alpha1.RemoveOutdatedAnnotation] = ""
		err := reconciler.client.Update(ctx, suite)
		Expect(err).To(BeNil())
	}

	shouldRemoveOutdatedRemediation := func() {
		rem := reconcileAndGetRemediation()
		Expect(rem.Spec.Apply).To(BeTrue())
		Expect(rem.Spec.Outdated.Object).To(BeNil())
	}

	Context("When reconciling generic remediations", func() {
		BeforeEach(func() {
			remediation := &compv1alpha1.ComplianceRemediation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      remediationName,
					Namespace: namespace,
					Labels: map[string]string{
						compv1alpha1.SuiteLabel:          suiteName,
						compv1alpha1.ComplianceScanLabel: "testScanNode",
					},
				},
				Spec: compv1alpha1.ComplianceRemediationSpec{
					ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
						Apply: false,
					},
					Current: compv1alpha1.ComplianceRemediationPayload{
						Object: nil,
					},
				},
			}
			cm := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
			}
			unstructuredCM, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
			Expect(err).ToNot(HaveOccurred())
			remediation.Spec.Current.Object = &unstructured.Unstructured{
				Object: unstructuredCM,
			}
			err = reconciler.client.Create(ctx, remediation.DeepCopy())
			Expect(err).To(BeNil())
		})

		reconcileShouldApplyTheRemediation := func() {
			By("Running a reconcile loop")
			rem := reconcileAndGetRemediation()
			Expect(rem.Spec.Apply).To(BeTrue())

			By("The remediation controller setting the applied status")
			rem.Status.ApplicationState = compv1alpha1.RemediationApplied
			err := reconciler.client.Update(ctx, rem)
			Expect(err).To(BeNil())

			By("Running a second reconcile loop")
			_, err = reconciler.reconcileRemediations(suite, logger)
			Expect(err).To(BeNil())
		}

		Context("With spec.AutoApplyRemediations = false", func() {
			It("Should leave the remediation unapplied", reconcileShouldNotApplyTheRemediation)
		})

		Context("With spec.AutoApplyRemediations = true", func() {
			BeforeEach(func() {
				suite.Spec.AutoApplyRemediations = true
				err := reconciler.client.Status().Update(ctx, suite)
				Expect(err).To(BeNil())
			})
			Context("With ComplianceSuite and Scans not done", func() {
				It("Should not apply the remediation", reconcileShouldNotApplyTheRemediation)
			})
			Context("With ComplianceSuite and Scans DONE", func() {
				BeforeEach(suiteAndScansInDonePhase)
				It("Should apply the remediation", reconcileShouldApplyTheRemediation)

				Context("With remove-outdated annotation", func() {
					BeforeEach(prepareForRemoveOutdatedScenarios)
					It("Should remove the outdated remediation and remove the annotation", func() {
						By("Reconciling the remediations")
						shouldRemoveOutdatedRemediation()

						By("Verifying the suite no longer has the remove-outdated annotation")
						key := types.NamespacedName{Name: suiteName, Namespace: namespace}
						s := &compv1alpha1.ComplianceSuite{}
						err := reconciler.client.Get(ctx, key, s)
						Expect(err).To(BeNil())
						Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.RemoveOutdatedAnnotation))
					})
				})
			})
		})

		Context("With apply-remediations annotation", func() {
			BeforeEach(func() {
				suite.Annotations = make(map[string]string, 2)
				suite.Annotations[compv1alpha1.ApplyRemediationsAnnotation] = ""
				err := reconciler.client.Update(ctx, suite)
				Expect(err).To(BeNil())
			})
			Context("With ComplianceSuite and Scans not done", func() {
				It("Should not apply the remediation", reconcileShouldNotApplyTheRemediation)
			})
			Context("With ComplianceSuite and Scans DONE", func() {
				BeforeEach(suiteAndScansInDonePhase)
				It("Should apply the remediation and remove the annotation", func() {
					By("Reconciling the remediations")
					reconcileShouldApplyTheRemediation()

					By("Verifying the suite no longer has the apply-remediation annotation")
					key := types.NamespacedName{Name: suiteName, Namespace: namespace}
					s := &compv1alpha1.ComplianceSuite{}
					err := reconciler.client.Get(ctx, key, s)
					Expect(err).To(BeNil())
					Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.ApplyRemediationsAnnotation))
				})

				Context("With remove-outdated annotation", func() {
					BeforeEach(prepareForRemoveOutdatedScenarios)
					It("Should remove the outdated remediation and remove the annotation", func() {
						By("Reconciling the remediations")
						shouldRemoveOutdatedRemediation()

						By("Verifying the suite no longer has the remove-outdated annotation")
						key := types.NamespacedName{Name: suiteName, Namespace: namespace}
						s := &compv1alpha1.ComplianceSuite{}
						err := reconciler.client.Get(ctx, key, s)
						Expect(err).To(BeNil())
						Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.RemoveOutdatedAnnotation))
					})
				})
			})
		})
	})

	Context("When reconciling MachineConfig remediations", func() {
		var poolName = "test-pool"
		BeforeEach(func() {
			mcp := &mcfgv1.MachineConfigPool{
				TypeMeta: metav1.TypeMeta{
					Kind:       "MachineConfigPool",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: poolName,
				},
				Spec: mcfgv1.MachineConfigPoolSpec{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: targetNodeSelector,
					},
				},
			}
			err := reconciler.client.Create(ctx, mcp)
			Expect(err).To(BeNil())

			remediation := &compv1alpha1.ComplianceRemediation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      remediationName,
					Namespace: namespace,
					Labels: map[string]string{
						compv1alpha1.SuiteLabel:          suiteName,
						compv1alpha1.ComplianceScanLabel: "testScanNode",
					},
				},
				Spec: compv1alpha1.ComplianceRemediationSpec{
					ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
						Apply: false,
					},
					Current: compv1alpha1.ComplianceRemediationPayload{
						Object: nil,
					},
				},
			}
			mc := &mcfgv1.MachineConfig{
				TypeMeta: metav1.TypeMeta{
					Kind:       "MachineConfig",
					APIVersion: mcfgapi.GroupName + "/v1",
				},
			}
			unstructuredMC, err := runtime.DefaultUnstructuredConverter.ToUnstructured(mc)
			Expect(err).ToNot(HaveOccurred())
			remediation.Spec.Current.Object = &unstructured.Unstructured{
				Object: unstructuredMC,
			}
			err = reconciler.client.Create(ctx, remediation.DeepCopy())
			Expect(err).To(BeNil())
		})

		reconcileShouldApplyTheRemediationAndHandlePausingPools := func() {
			By("Running a reconcile loop")
			rem := reconcileAndGetRemediation()
			Expect(rem.Spec.Apply).To(BeTrue())

			By("The remediation controller setting the applied status")
			rem.Status.ApplicationState = compv1alpha1.RemediationApplied
			err := reconciler.client.Update(ctx, rem)
			Expect(err).To(BeNil())

			By("the pool should be paused")
			p := &mcfgv1.MachineConfigPool{}
			poolkey := types.NamespacedName{Name: poolName}
			err = reconciler.client.Get(ctx, poolkey, p)
			Expect(err).To(BeNil())
			Expect(p.Spec.Paused).To(BeTrue())

			By("Running a second reconcile loop")
			_, err = reconciler.reconcileRemediations(suite, logger)
			Expect(err).To(BeNil())

			By("the pool should be un-paused")
			err = reconciler.client.Get(ctx, poolkey, p)
			Expect(err).To(BeNil())
			Expect(p.Spec.Paused).To(BeFalse())
		}

		Context("With spec.AutoApplyRemediations = true", func() {
			BeforeEach(func() {
				suite.Spec.AutoApplyRemediations = true
				err := reconciler.client.Status().Update(ctx, suite)
				Expect(err).To(BeNil())
			})
			Context("With ComplianceSuite and Scans not done", func() {
				It("Should not apply the remediation", reconcileShouldNotApplyTheRemediation)
			})
			Context("With ComplianceSuite and Scans DONE", func() {
				BeforeEach(suiteAndScansInDonePhase)
				It("Should apply the remediation", reconcileShouldApplyTheRemediationAndHandlePausingPools)

				Context("With remove-outdated annotation", func() {
					BeforeEach(prepareForRemoveOutdatedScenarios)
					It("Should remove the outdated remediation and remove the annotation", func() {
						By("Reconciling the remediations")
						shouldRemoveOutdatedRemediation()

						By("Verifying the suite no longer has the remove-outdated annotation")
						key := types.NamespacedName{Name: suiteName, Namespace: namespace}
						s := &compv1alpha1.ComplianceSuite{}
						err := reconciler.client.Get(ctx, key, s)
						Expect(err).To(BeNil())
						Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.RemoveOutdatedAnnotation))
					})
				})
			})
		})

		Context("With apply-remediations annotation", func() {
			BeforeEach(func() {
				suite.Annotations = make(map[string]string, 2)
				suite.Annotations[compv1alpha1.ApplyRemediationsAnnotation] = ""
				err := reconciler.client.Update(ctx, suite)
				Expect(err).To(BeNil())
			})
			Context("With ComplianceSuite and Scans not done", func() {
				It("Should not apply the remediation", reconcileShouldNotApplyTheRemediation)
			})
			Context("With ComplianceSuite and Scans DONE", func() {
				BeforeEach(suiteAndScansInDonePhase)
				It("Should apply the remediation and remove the annotation", func() {
					By("Reconciling the remediations")
					reconcileShouldApplyTheRemediationAndHandlePausingPools()

					By("Verifying the suite no longer has the apply-remediation annotation")
					key := types.NamespacedName{Name: suiteName, Namespace: namespace}
					s := &compv1alpha1.ComplianceSuite{}
					err := reconciler.client.Get(ctx, key, s)
					Expect(err).To(BeNil())
					Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.ApplyRemediationsAnnotation))
				})

				Context("With remove-outdated annotation", func() {
					BeforeEach(prepareForRemoveOutdatedScenarios)
					It("Should remove the outdated remediation and remove the annotation", func() {
						By("Reconciling the remediations")
						shouldRemoveOutdatedRemediation()

						By("Verifying the suite no longer has the remove-outdated annotation")
						key := types.NamespacedName{Name: suiteName, Namespace: namespace}
						s := &compv1alpha1.ComplianceSuite{}
						err := reconciler.client.Get(ctx, key, s)
						Expect(err).To(BeNil())
						Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.RemoveOutdatedAnnotation))
					})
				})
			})
		})
	})
	// testing for KC remediation

	Context("When reconciling KubeletConfig remediations", func() {
		var poolName = "test-pool"
		remediationKCPayload := `
			{
				"something": "0s"
			}
			`
		remediationKCMCPayload := `
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

		BeforeEach(func() {
			mcp := &mcfgv1.MachineConfigPool{
				TypeMeta: metav1.TypeMeta{
					Kind:       "MachineConfigPool",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: poolName,
				},
				Spec: mcfgv1.MachineConfigPoolSpec{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: targetNodeSelector,
					},
					Configuration: mcfgv1.MachineConfigPoolStatusConfiguration{
						Source: []corev1.ObjectReference{{Name: "99-master-generated-kubelet"}},
					},
				},
			}
			err := reconciler.client.Create(ctx, mcp)
			Expect(err).To(BeNil())

			remediation := &compv1alpha1.ComplianceRemediation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      remediationName,
					Namespace: namespace,
					Labels: map[string]string{
						compv1alpha1.SuiteLabel:          suiteName,
						compv1alpha1.ComplianceScanLabel: "testScanNode",
					},
				},
				Spec: compv1alpha1.ComplianceRemediationSpec{
					ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
						Apply: false,
					},
					Current: compv1alpha1.ComplianceRemediationPayload{
						Object: nil,
					},
				},
			}
			kcPayload := `{"apiVersion": "machineconfiguration.openshift.io/v1","kind": "KubeletConfig","spec": {"kubeletConfig": {"streamingConnectionIdleTimeout": "0s","something": "0s"}}}`

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
			//prepare machine config for testing
			kcOwnerRef := metav1.OwnerReference{
				APIVersion: "machineconfiguration.openshift.io/v1",
				Kind:       "KubeletConfig",
				Name:       "kubelet-config-compliance-operator",
				UID:        "12345",
			}

			mc := &mcfgv1.MachineConfig{
				TypeMeta: metav1.TypeMeta{
					Kind:       "MachineConfig",
					APIVersion: "machineconfiguration.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{kcOwnerRef},
					Name:            "99-master-generated-kubelet",
				},
				Spec: mcfgv1.MachineConfigSpec{
					Config: runtime.RawExtension{
						Raw: []byte(renderdKC),
					},
				},
			}
			err = reconciler.client.Create(ctx, mc)
			Expect(err).To(BeNil())

			existingKCPayload := `
			{
				"streamingConnectionIdleTimeout": "0s"
			}
			`
			existingKCObj := &mcfgv1.KubeletConfig{
				TypeMeta: metav1.TypeMeta{
					Kind:       "KubeletConfig",
					APIVersion: "machineconfiguration.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "kubelet-config-compliance-operator",
				},
				Spec: mcfgv1.KubeletConfigSpec{
					KubeletConfig: &runtime.RawExtension{
						Raw: []byte(existingKCPayload),
					},
				},
			}
			//create existing kubelet config
			err = reconciler.client.Create(ctx, existingKCObj.DeepCopy())
			Expect(err).To(BeNil())

			var obj map[string]interface{}
			err = json.Unmarshal([]byte(kcPayload), &obj)
			Expect(err).ToNot(HaveOccurred())
			remediation.Spec.Current.Object = &unstructured.Unstructured{
				Object: obj,
			}
			err = reconciler.client.Create(ctx, remediation.DeepCopy())
			Expect(err).To(BeNil())

		})

		reconcileShouldApplyTheRemediationAndHandlePausingPools := func() {
			By("Running a reconcile loop")
			rem := reconcileAndGetRemediation()
			Expect(rem.Spec.Apply).To(BeTrue())

			By("The remediation controller setting the applied status")
			rem.Status.ApplicationState = compv1alpha1.RemediationApplied
			err := reconciler.client.Update(ctx, rem)
			Expect(err).To(BeNil())

			By("the pool should be paused")
			p := &mcfgv1.MachineConfigPool{}
			poolkey := types.NamespacedName{Name: poolName}
			err = reconciler.client.Get(ctx, poolkey, p)
			Expect(err).To(BeNil())
			Expect(p.Spec.Paused).To(BeTrue())

			By("The remediation controller should create/patch the kubelet config object")
			kc := &mcfgv1.KubeletConfig{}
			kckey := types.NamespacedName{Name: "kubelet-config-compliance-operator"}
			err = reconciler.client.Get(ctx, kckey, kc)
			Expect(err).To(BeNil())
			kc.Spec.KubeletConfig.Raw = []byte(remediationKCPayload)
			err = reconciler.client.Patch(ctx, kc, client.Merge)
			Expect(err).To(BeNil())

			By("Running a second reconcile loop")
			_, err = reconciler.reconcileRemediations(suite, logger)
			Expect(err).To(BeNil())

			By("the pool should not be un-paused because the KubeletConfig is not rendered into Machine Config")
			err = reconciler.client.Get(ctx, poolkey, p)
			Expect(err).To(BeNil())
			Expect(p.Spec.Paused).To(BeTrue())

			By("Render KubeLetconfig into Machine Config")
			mcCurrent := &mcfgv1.MachineConfig{}
			mckey := types.NamespacedName{Name: "99-master-generated-kubelet"}
			err = reconciler.client.Get(ctx, mckey, mcCurrent)
			Expect(err).To(BeNil())
			mcCurrent.Spec.Config.Raw = []byte(remediationKCMCPayload)
			err = reconciler.client.Update(ctx, mcCurrent)
			Expect(err).To(BeNil())

			By("Running a second reconcile loop")
			_, err = reconciler.reconcileRemediations(suite, logger)
			Expect(err).To(BeNil())

			By("the pool should be un-paused because machine config has been updated with the new kubelet config content")
			err = reconciler.client.Get(ctx, poolkey, p)
			Expect(err).To(BeNil())
			Expect(p.Spec.Paused).To(BeFalse())

			s := &compv1alpha1.ComplianceRemediation{}
			key := types.NamespacedName{Name: remediationName, Namespace: namespace}
			reconciler.client.Get(ctx, key, s)

		}

		Context("With spec.AutoApplyRemediations = true", func() {
			BeforeEach(func() {
				suite.Spec.AutoApplyRemediations = true
				err := reconciler.client.Status().Update(ctx, suite)
				Expect(err).To(BeNil())
			})
			Context("With ComplianceSuite and Scans not done", func() {
				It("Should not apply the remediation", reconcileShouldNotApplyTheRemediation)
			})
			Context("With ComplianceSuite and Scans DONE", func() {
				BeforeEach(suiteAndScansInDonePhase)
				It("Should apply the remediation", reconcileShouldApplyTheRemediationAndHandlePausingPools)

				Context("With remove-outdated annotation", func() {
					BeforeEach(prepareForRemoveOutdatedScenarios)
					It("Should remove the outdated remediation and remove the annotation", func() {
						By("Reconciling the remediations")
						shouldRemoveOutdatedRemediation()

						By("Verifying the suite no longer has the remove-outdated annotation")
						key := types.NamespacedName{Name: suiteName, Namespace: namespace}
						s := &compv1alpha1.ComplianceSuite{}
						err := reconciler.client.Get(ctx, key, s)
						Expect(err).To(BeNil())
						Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.RemoveOutdatedAnnotation))
					})
				})
			})
		})

		Context("With apply-remediations annotation", func() {
			BeforeEach(func() {
				suite.Annotations = make(map[string]string, 2)
				suite.Annotations[compv1alpha1.ApplyRemediationsAnnotation] = ""
				err := reconciler.client.Update(ctx, suite)
				Expect(err).To(BeNil())
			})
			Context("With ComplianceSuite and Scans not done", func() {
				It("Should not apply the remediation", reconcileShouldNotApplyTheRemediation)
			})
			Context("With ComplianceSuite and Scans DONE", func() {
				BeforeEach(suiteAndScansInDonePhase)
				It("Should apply the remediation and remove the annotation", func() {
					By("Reconciling the remediations")
					reconcileShouldApplyTheRemediationAndHandlePausingPools()

					By("Verifying the suite no longer has the apply-remediation annotation")
					key := types.NamespacedName{Name: suiteName, Namespace: namespace}
					s := &compv1alpha1.ComplianceSuite{}
					err := reconciler.client.Get(ctx, key, s)
					Expect(err).To(BeNil())
					Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.ApplyRemediationsAnnotation))
				})

				Context("With remove-outdated annotation", func() {
					BeforeEach(prepareForRemoveOutdatedScenarios)
					It("Should remove the outdated remediation and remove the annotation", func() {
						By("Reconciling the remediations")
						shouldRemoveOutdatedRemediation()

						By("Verifying the suite no longer has the remove-outdated annotation")
						key := types.NamespacedName{Name: suiteName, Namespace: namespace}
						s := &compv1alpha1.ComplianceSuite{}
						err := reconciler.client.Get(ctx, key, s)
						Expect(err).To(BeNil())
						Expect(s.Annotations).ToNot(HaveKey(compv1alpha1.RemoveOutdatedAnnotation))
					})
				})
			})
		})
	})

})
