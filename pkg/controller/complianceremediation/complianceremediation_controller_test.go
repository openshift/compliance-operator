package complianceremediation

import (
	"context"
	"strings"

	"github.com/clarketm/json"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/compliance-operator/pkg/apis"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/metrics"
	"github.com/openshift/compliance-operator/pkg/controller/metrics/metricsfakes"
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
)

var _ = Describe("Testing complianceremediation controller", func() {

	var (
		remediationinstance *compv1alpha1.ComplianceRemediation
		mcp                 *mcfgv1.MachineConfigPool
		scanInstance        *compv1alpha1.ComplianceScan
		reconciler          *ReconcileComplianceRemediation
		testRemLabels       map[string]string
		testRemAnnotations  map[string]string
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

		testRemAnnotations = make(map[string]string)
		testRemAnnotations[compv1alpha1.RemediationValueUsedAnnotation] = "var-used-1"
		testRemAnnotations[compv1alpha1.RemediationUnsetValueAnnotation] = "var-unset-1"
		testRemAnnotations[compv1alpha1.RemediationValueRequiredAnnotation] = "req-value-1"
		nodeLabels := map[string]string{
			mcfgv1.MachineConfigRoleLabelKey: "myRole",
		}

		mcp = &mcfgv1.MachineConfigPool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-pool",
			},
			Spec: mcfgv1.MachineConfigPoolSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: nodeLabels,
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
							Name:       "something-else",
						},
					},
				},
			},
		}

		// test instance
		remediationinstance = &compv1alpha1.ComplianceRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "testRem",
				Labels:      testRemLabels,
				Annotations: testRemAnnotations,
			},
			Spec: compv1alpha1.ComplianceRemediationSpec{
				Current: compv1alpha1.ComplianceRemediationPayload{
					Object: nil,
				},
			},
		}
		remediationinstance.Spec.Type = compv1alpha1.ConfigurationRemediation
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
		mockMetrics := metrics.NewMetrics(&metricsfakes.FakeImpl{})
		err = mockMetrics.Register()
		Expect(err).To(BeNil())

		reconciler = &ReconcileComplianceRemediation{client: client, scheme: cscheme, metrics: mockMetrics}
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

		Context("with current KubeletConfig remediation object and default no custom kubelet config", func() {
			BeforeEach(func() {

				rawConfig, _ := json.Marshal(map[string]int{"maxPods": 1123})
				kc := &mcfgv1.KubeletConfig{
					TypeMeta: metav1.TypeMeta{
						Kind:       "KubeletConfig",
						APIVersion: mcfgapi.GroupName + "/v1",
					},
					// We specifically add no ObjectMeta since this will be
					// added by the operator
					Spec: mcfgv1.KubeletConfigSpec{
						KubeletConfig: &runtime.RawExtension{
							Raw: rawConfig,
						},
					},
				}
				unstructuredKC, err := runtime.DefaultUnstructuredConverter.ToUnstructured(kc)
				Expect(err).ToNot(HaveOccurred())
				remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
					Object: unstructuredKC,
				}
				err = reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reconcile the current remediation", func() {
				By("running a reconcile loop")

				err := reconciler.reconcileRemediation(remediationinstance, logger)
				Expect(err).To(BeNil())

				By("the remediation should be applied")
				foundKC := &mcfgv1.KubeletConfig{}
				mcKey := types.NamespacedName{Name: "compliance-operator-kubelet-" + mcp.GetName()}
				err = reconciler.client.Get(context.TODO(), mcKey, foundKC)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("with current KubeletConfig remediation object and many custom kubelet configs", func() {
			BeforeEach(func() {
				//Setting environment with mutiple custom kubelet config
				mcConfig := mcfgv1.MachineConfigPoolStatusConfiguration{
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
				}
				mcp.Spec.Configuration = mcConfig
				err := reconciler.client.Update(context.TODO(), mcp)
				Expect(err).NotTo(HaveOccurred())

				//We need to simulate for a exsisting generated kubelet Machine Config
				kMCGenerated := &mcfgv1.MachineConfig{
					TypeMeta: metav1.TypeMeta{
						Kind:       "MachineConfig",
						APIVersion: mcfgapi.GroupName + "/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "99-worker-generated-kubelet-3",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "machineconfiguration.openshift.io/v1",
								Kind:       "KubeletConfig",
								Name:       "custom-kubelet",
								UID:        "b144e060-d19f-443c-81e1-813422aa78be",
							},
						},
					},
				}

				err = reconciler.client.Create(context.TODO(), kMCGenerated)
				Expect(err).NotTo(HaveOccurred())

				//create the exsisting kublet config, owner of 99-worker-generated-kubelet-3
				oriRawConfig, _ := json.Marshal(map[string]int{"maxPods": 1100})

				ekc := &mcfgv1.KubeletConfig{
					TypeMeta: metav1.TypeMeta{
						Kind:       "KubeletConfig",
						APIVersion: mcfgapi.GroupName + "/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "custom-kubelet",
						Annotations: map[string]string{
							"compliance.openshift.io/remediation": "",
						},
					},
					Spec: mcfgv1.KubeletConfigSpec{
						KubeletConfig: &runtime.RawExtension{
							Raw: oriRawConfig,
						},
					},
				}

				err = reconciler.client.Create(context.TODO(), ekc)
				Expect(err).NotTo(HaveOccurred())

				newRawConfig, _ := json.Marshal(map[string]int{"test-value-2": 1125})
				kc := &mcfgv1.KubeletConfig{
					TypeMeta: metav1.TypeMeta{
						Kind:       "KubeletConfig",
						APIVersion: mcfgapi.GroupName + "/v1",
					},
					// We specifically add no ObjectMeta since this will be
					// added by the operator
					Spec: mcfgv1.KubeletConfigSpec{
						KubeletConfig: &runtime.RawExtension{
							Raw: newRawConfig,
						},
					},
				}
				unstructuredKC, err := runtime.DefaultUnstructuredConverter.ToUnstructured(kc)
				Expect(err).ToNot(HaveOccurred())
				remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
					Object: unstructuredKC,
				}
				err = reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should reconcile the current remediation", func() {
				By("running a reconcile loop")

				err := reconciler.reconcileRemediation(remediationinstance, logger)
				Expect(err).To(BeNil())

				By("the remediation should be applied")
				foundKC := &mcfgv1.KubeletConfig{}
				mcKey := types.NamespacedName{Name: "custom-kubelet"}
				err = reconciler.client.Get(context.TODO(), mcKey, foundKC)
				Expect(err).ToNot(HaveOccurred())

				By("Unapply remediation")
				remediationinstance.Spec.Apply = false
				err = reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())

				err = reconciler.reconcileRemediation(remediationinstance, logger)
				Expect(err).To(BeNil())

				By("should not return a NotFound error")
				// we don't allow kubeletconfig remediation to be unapplied
				foundKC = &mcfgv1.KubeletConfig{}
				mcKey = types.NamespacedName{Name: "custom-kubelet"}
				err = reconciler.client.Get(context.TODO(), mcKey, foundKC)
				Expect(kerrors.IsNotFound(err)).NotTo(BeTrue())
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
		Context("Test handle unse value", func() {
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

			It("Test annotation with empty string", func() {
				testRemAnnotations = make(map[string]string)
				testRemAnnotations[compv1alpha1.RemediationValueUsedAnnotation] = ""
				testRemAnnotations[compv1alpha1.RemediationUnsetValueAnnotation] = ""
				testRemAnnotations[compv1alpha1.RemediationValueRequiredAnnotation] = ""
				remediationinstance.Annotations = testRemAnnotations

				err := reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())

				key := types.NamespacedName{Name: remediationinstance.GetName()}
				hasUpdate, err := reconciler.handleUnsetValues(remediationinstance, logger)
				Expect(err).To(HaveOccurred())
				Expect(hasUpdate).To(Equal(false))

				By("running a reconcile loop")
				err = reconciler.client.Get(context.TODO(), key, remediationinstance)
				Expect(err).ToNot(HaveOccurred())

				hasUpdate, err = reconciler.handleValueRequired(remediationinstance, logger)
				Expect(err).To(HaveOccurred())
				Expect(hasUpdate).To(Equal(false))

			})

		})
		Context("Handle Unset-value", func() {
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
				unstructuredCurrent, err := runtime.DefaultUnstructuredConverter.ToUnstructured(currentcm)
				Expect(err).ToNot(HaveOccurred())
				remediationinstance.Spec.Current.Object = &unstructured.Unstructured{
					Object: unstructuredCurrent,
				}
				err = reconciler.client.Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
				remediationinstance.Status.ApplicationState = compv1alpha1.RemediationApplied
				err = reconciler.client.Status().Update(context.TODO(), remediationinstance)
				Expect(err).NotTo(HaveOccurred())
			})
			It("should add unset-label", func() {
				By("running a reconcile loop")
				key := types.NamespacedName{Name: remediationinstance.GetName()}
				req := reconcile.Request{
					NamespacedName: key,
				}
				_, err := reconciler.Reconcile(req)
				Expect(err).To(BeNil())
				By("The unset-label should be added")
				foundRem := &compv1alpha1.ComplianceRemediation{}
				err = reconciler.client.Get(context.TODO(), key, foundRem)
				Expect(err).ToNot(HaveOccurred())
				Expect(foundRem.Labels).To(HaveKey(compv1alpha1.RemediationUnsetValueLabel))
			})
			//Because nothing will be found in TailorProfile, the variables in RemediationValueRequiredAnnotation will be added into
			//RemediationUnsetValueAnnotation
			It("should add value-required value to unset-annotations", func() {
				By("running a reconcile loop first time will add label")
				key := types.NamespacedName{Name: remediationinstance.GetName()}
				req := reconcile.Request{
					NamespacedName: key,
				}
				_, err := reconciler.Reconcile(req)
				Expect(err).To(BeNil())
				By("running a reconcile loop second time will handle value-required annotation")
				_, err = reconciler.Reconcile(req)
				Expect(err).To(BeNil())
				By("running a reconcile loop third time should update the status to Needs-Review")
				_, err = reconciler.Reconcile(req)
				Expect(err).To(BeNil())
				By("The unset-label should be added")
				foundRem := &compv1alpha1.ComplianceRemediation{}
				err = reconciler.client.Get(context.TODO(), key, foundRem)
				Expect(err).ToNot(HaveOccurred())
				unSetVals := strings.Split(foundRem.Annotations[compv1alpha1.RemediationUnsetValueAnnotation], ",")
				Expect(unSetVals).To(ContainElement("req-value-1"))
				By("The RequiredProcessedLabel should be added ")
				Expect(foundRem.Labels).To(HaveKey(compv1alpha1.RemediationValueRequiredProcessedLabel))
				By("The status should be NeedsReview")
				Expect(foundRem.Status.ApplicationState).To(Equal(compv1alpha1.RemediationNeedsReview))
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
