package complianceremediation

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/compliance-operator/pkg/apis"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

var _ = Describe("Testing complianceremediation controller", func() {

	var (
		remediationinstance *compv1alpha1.ComplianceRemediation
		scanInstance        *compv1alpha1.ComplianceScan
		reconciler          *ReconcileComplianceRemediation
		testRemLabels       map[string]string
		logger              logr.Logger
	)

	itShouldNotReconcile := func() {
		It("should not reconcile the remediation", func() {
			err := reconciler.reconcileRemediation(remediationinstance, logger)
			Expect(err).ToNot(BeNil())
		})
	}

	BeforeEach(func() {
		objs := []runtime.Object{}

		testRemLabels = make(map[string]string)
		testRemLabels[compv1alpha1.SuiteLabel] = "mySuite"
		testRemLabels[compv1alpha1.ComplianceScanLabel] = "myScan"

		nodeLabels := map[string]string{
			mcfgv1.MachineConfigRoleLabelKey: "myRole",
		}

		mcp := &mcfgv1.MachineConfigPool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-pool",
			},
			Spec: mcfgv1.MachineConfigPoolSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: nodeLabels,
				},
			},
		}

		// test instance
		remediationinstance = &compv1alpha1.ComplianceRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "testRem",
				Labels: testRemLabels,
			},
			Spec: compv1alpha1.ComplianceRemediationSpec{
				Current: compv1alpha1.ComplianceRemediationPayload{
					Object: nil,
				},
			},
		}
		scanInstance = &compv1alpha1.ComplianceScan{
			ObjectMeta: metav1.ObjectMeta{
				Name: "myScan",
			},
			Spec: compv1alpha1.ComplianceScanSpec{
				NodeSelector: nodeLabels,
			},
		}
		objs = append(objs, remediationinstance, scanInstance, mcp)

		cscheme := scheme.Scheme
		err := apis.AddToScheme(cscheme)
		Expect(err).To(BeNil())
		err = mcfgapi.Install(cscheme)
		Expect(err).To(BeNil())

		client := fake.NewFakeClientWithScheme(cscheme, objs...)
		reconciler = &ReconcileComplianceRemediation{client: client, scheme: cscheme}
		zaplog, _ := zap.NewDevelopment()
		logger = zapr.NewLogger(zaplog)
	})

	Context("applying remediations", func() {
		BeforeEach(func() {
			remediationinstance.Spec.Apply = true
			reconciler.client.Update(context.TODO(), remediationinstance)
		})

		Context("with a nil object", itShouldNotReconcile)

		Context("with current ConfigMap remediation object", func() {
			BeforeEach(func() {
				cm := &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-cm",
						Namespace: "test-ns",
					},
					Data: map[string]string{
						"key": "val",
					},
				}
				unstructuredCM, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
				Expect(err).ToNot(HaveOccurred())
				remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
					Object: unstructuredCM,
				}
				err = reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reconcile the current remediation", func() {
				By("running a reconcile loop")

				err := reconciler.reconcileRemediation(remediationinstance, logger)
				Expect(err).To(BeNil())

				By("the remediation should be applied")
				foundCM := &corev1.ConfigMap{}
				err = reconciler.client.Get(context.TODO(), types.NamespacedName{Name: "my-cm", Namespace: "test-ns"}, foundCM)
				Expect(err).ToNot(HaveOccurred())
				Expect(foundCM.GetName()).To(Equal("my-cm"))
				Expect(foundCM.Data["key"]).To(Equal("val"))
			})
		})

		Context("with current MachineConfig remediation object", func() {
			BeforeEach(func() {
				mc := &mcfgv1.MachineConfig{
					TypeMeta: metav1.TypeMeta{
						Kind:       "MachineConfig",
						APIVersion: mcfgapi.GroupName + "/v1",
					},
					// We specifically add no ObjectMeta since this will be
					// added by the operator
					Spec: mcfgv1.MachineConfigSpec{
						FIPS: true,
					},
				}
				unstructuredMC, err := runtime.DefaultUnstructuredConverter.ToUnstructured(mc)
				Expect(err).ToNot(HaveOccurred())
				remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
					Object: unstructuredMC,
				}
				err = reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reconcile the current remediation", func() {
				By("running a reconcile loop")

				err := reconciler.reconcileRemediation(remediationinstance, logger)
				Expect(err).To(BeNil())

				By("the remediation should be applied")
				foundMC := &mcfgv1.MachineConfig{}
				mcKey := types.NamespacedName{Name: remediationinstance.GetMcName()}
				err = reconciler.client.Get(context.TODO(), mcKey, foundMC)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("with an outdated remediation object", func() {
			BeforeEach(func() {
				currentcm := &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-cm",
						Namespace: "test-ns",
					},
					Data: map[string]string{
						"currentkey": "currentval",
					},
				}
				outdatedcm := &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-cm",
						Namespace: "test-ns",
					},
					Data: map[string]string{
						"outdatedkey": "outdatedval",
					},
				}
				unstructuredCurrent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(currentcm)
				Expect(err).ToNot(HaveOccurred())
				unstructuredOutdated, err := runtime.DefaultUnstructuredConverter.ToUnstructured(outdatedcm)
				Expect(err).ToNot(HaveOccurred())
				remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
					Object: unstructuredCurrent,
				}
				remediationinstance.Spec.Outdated.Object = &unstructured.Unstructured{
					Object: unstructuredOutdated,
				}
				err = reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reconcile the outdated remediation", func() {
				By("running a reconcile loop")

				err := reconciler.reconcileRemediation(remediationinstance, logger)
				Expect(err).To(BeNil())

				By("the outdated remediation should be applied")
				foundCM := &corev1.ConfigMap{}
				err = reconciler.client.Get(context.TODO(), types.NamespacedName{Name: "my-cm", Namespace: "test-ns"}, foundCM)
				Expect(err).ToNot(HaveOccurred())
				Expect(foundCM.GetName()).To(Equal("my-cm"))
				Expect(foundCM.Data["outdatedkey"]).To(Equal("outdatedval"))
			})
		})

		Context("with handled outdated remediation object", func() {
			BeforeEach(func() {
				remediationinstance.Labels[compv1alpha1.OutdatedRemediationLabel] = ""
				currentcm := &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-cm",
						Namespace: "test-ns",
					},
					Data: map[string]string{
						"currentkey": "currentval",
					},
				}
				unstructuredCurrent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(currentcm)
				Expect(err).ToNot(HaveOccurred())
				remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
					Object: unstructuredCurrent,
				}
				// NOTE that the Outdated remediation object is nil, which
				// reflects an admin having removed it.
				err = reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
				// mock that the remediation was applied
				remediationinstance.Status.ApplicationState = compv1alpha1.RemediationApplied
				err = reconciler.client.Status().Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should remove the outdated remediation label", func() {
				By("running a reconcile loop")

				key := types.NamespacedName{Name: remediationinstance.GetName()}
				req := reconcile.Request{
					NamespacedName: key,
				}
				_, err := reconciler.Reconcile(req)
				Expect(err).To(BeNil())

				By("the outdated remediation label should not be there")
				foundRem := &compv1alpha1.ComplianceRemediation{}
				err = reconciler.client.Get(context.TODO(), key, foundRem)
				Expect(err).ToNot(HaveOccurred())
				Expect(foundRem.Labels).NotTo(HaveKey(compv1alpha1.OutdatedRemediationLabel))
			})
		})
	})

	Context("un-applying remediations", func() {
		BeforeEach(func() {
			remediationinstance.Spec.Apply = false
			err := reconciler.client.Update(context.TODO(), remediationinstance)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("with a nil object", itShouldNotReconcile)

		Context("with no existing remediation", func() {
			Context("with a current remediation object", func() {
				BeforeEach(func() {
					cm := &corev1.ConfigMap{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ConfigMap",
							APIVersion: "v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "my-cm",
							Namespace: "test-ns",
						},
						Data: map[string]string{
							"key": "val",
						},
					}
					unstructuredCM, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
					Expect(err).ToNot(HaveOccurred())
					remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
						Object: unstructuredCM,
					}
					err = reconciler.client.Update(context.TODO(), remediationinstance)
					Expect(err).NotTo(HaveOccurred())
				})

				It("should do nothing", func() {
					By("running a reconcile loop")

					err := reconciler.reconcileRemediation(remediationinstance, logger)
					Expect(err).To(BeNil())

					By("the remediation should not be applied")
					foundCM := &corev1.ConfigMap{}
					err = reconciler.client.Get(context.TODO(), types.NamespacedName{Name: "my-cm", Namespace: "test-ns"}, foundCM)
					By("should return a NotFound error")
					Expect(kerrors.IsNotFound(err)).To(BeTrue())
				})
			})
		})

		Context("with an existing remediation", func() {
			Context("with a current remediation object", func() {
				BeforeEach(func() {
					cm := &corev1.ConfigMap{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ConfigMap",
							APIVersion: "v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "my-cm",
							Namespace: "test-ns",
						},
						Data: map[string]string{
							"key": "val",
						},
					}
					// Mark the object as created by the operator
					compv1alpha1.AddRemediationAnnotation(cm)
					unstructuredCM, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
					Expect(err).ToNot(HaveOccurred())
					remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
						Object: unstructuredCM,
					}
					err = reconciler.client.Update(context.TODO(), remediationinstance)
					Expect(err).NotTo(HaveOccurred())
					err = reconciler.client.Create(context.TODO(), cm)
					Expect(err).NotTo(HaveOccurred())
				})

				It("should delete the remediation", func() {
					By("checking that the object is there")
					foundCM := &corev1.ConfigMap{}
					err := reconciler.client.Get(context.TODO(), types.NamespacedName{Name: "my-cm", Namespace: "test-ns"}, foundCM)
					Expect(err).NotTo(HaveOccurred())
					Expect(foundCM.GetName()).To(Equal("my-cm"))
					Expect(foundCM.Data["key"]).To(Equal("val"))

					By("then running a reconcile loop")

					err = reconciler.reconcileRemediation(remediationinstance, logger)
					Expect(err).To(BeNil())

					By("the remediation should be un-applied")
					err = reconciler.client.Get(context.TODO(), types.NamespacedName{Name: "my-cm", Namespace: "test-ns"}, foundCM)
					By("should return a NotFound error")
					Expect(kerrors.IsNotFound(err)).To(BeTrue())
				})
			})
		})
	})
})
