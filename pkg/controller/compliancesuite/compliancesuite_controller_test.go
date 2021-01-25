package compliancesuite

import (
	"context"

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
		reconciler = &ReconcileComplianceSuite{client: client, scheme: cscheme}
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
})
