package scansettingbinding

import (
	"context"

	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Testing scansettingbinding controller", func() {

	var (
		reconciler ReconcileScanSettingBinding

		pBundleRhcos *compv1alpha1.ProfileBundle
		profRhcosE8  *compv1alpha1.Profile
		tpRhcosE8    *compv1alpha1.TailoredProfile

		setting *compv1alpha1.ScanSetting
		ssb     *compv1alpha1.ScanSettingBinding

		masterSelector map[string]string
		workerSelector map[string]string

		suite *compv1alpha1.ComplianceSuite
	)

	BeforeEach(func() {
		// Uncomment these lines if you need to debug the controller's output.
		dev, _ := zap.NewDevelopment()
		log = zapr.NewLogger(dev)
		objs := []runtime.Object{}

		// test instance
		ssb = &compv1alpha1.ScanSettingBinding{}
		suite = &compv1alpha1.ComplianceSuite{}

		platformProfileAnnotations := map[string]string{
			compv1alpha1.ProductTypeAnnotation: string(compv1alpha1.ScanTypeNode),
			compv1alpha1.ProductAnnotation:     "rhcos4",
		}

		pBundleRhcos = &compv1alpha1.ProfileBundle{
			ObjectMeta: v1.ObjectMeta{
				Name:      "rhcos4",
				Namespace: common.GetComplianceOperatorNamespace(),
			},
			Spec: compv1alpha1.ProfileBundleSpec{
				ContentImage: "quay.io/complianceascode/ocp4:latest",
				ContentFile:  "ssg-rhcos4-ds.xml",
			},
			Status: compv1alpha1.ProfileBundleStatus{
				DataStreamStatus: compv1alpha1.DataStreamValid,
			},
		}

		profRhcosE8 = &compv1alpha1.Profile{
			ObjectMeta: v1.ObjectMeta{
				Name:        "rhcos4-e8",
				Namespace:   common.GetComplianceOperatorNamespace(),
				Annotations: platformProfileAnnotations,
			},
			ProfilePayload: compv1alpha1.ProfilePayload{
				Title:       "rhcos4 profile",
				Description: "rhcos4 profile description",
				ID:          "xccdf_org.ssgproject.content_profile_e8",
			},
		}

		tpRhcosE8 = &compv1alpha1.TailoredProfile{
			ObjectMeta: v1.ObjectMeta{
				Name:      "emptypass-rhcos4-e8",
				Namespace: common.GetComplianceOperatorNamespace(),
				Labels:    platformProfileAnnotations,
			},
			Spec: compv1alpha1.TailoredProfileSpec{
				Extends:     profRhcosE8.Name,
				Title:       "testing TP",
				Description: "some desc",
				DisableRules: []compv1alpha1.RuleReferenceSpec{
					{
						Name:      "rhcos4-no-empty-passwords",
						Rationale: "I don't want this rule",
					},
				},
			},
			Status: compv1alpha1.TailoredProfileStatus{
				ID: "xccdf_compliance.openshift.io_profile_emptypass-rhcos4-e8",
				OutputRef: compv1alpha1.OutputRef{
					Name:      "emptypass-rhcos4-e8-tp",
					Namespace: common.GetComplianceOperatorNamespace(),
				},
				State:        compv1alpha1.TailoredProfileStateReady,
				ErrorMessage: "",
			},
		}

		setting = &compv1alpha1.ScanSetting{
			ObjectMeta: v1.ObjectMeta{
				Name:      "scan-setting",
				Namespace: common.GetComplianceOperatorNamespace(),
			},
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: true,
				Schedule:              "0 1 * * *",
			},
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
			Roles: []string{"master", "worker"},
		}

		objs = append(objs, ssb, pBundleRhcos, profRhcosE8, tpRhcosE8, suite, setting)

		scheme := scheme.Scheme
		scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion, objs...)

		client := fake.NewFakeClientWithScheme(scheme, pBundleRhcos, setting)

		err := client.Get(context.TODO(), types.NamespacedName{
			Namespace: pBundleRhcos.Namespace,
			Name:      pBundleRhcos.Name,
		}, pBundleRhcos)
		Expect(err).To(BeNil())

		profRhcosE8.OwnerReferences = append(profRhcosE8.OwnerReferences,
			v1.OwnerReference{
				Name:       pBundleRhcos.Name,
				Kind:       pBundleRhcos.Kind,
				APIVersion: pBundleRhcos.APIVersion})
		err = client.Create(context.TODO(), profRhcosE8)
		Expect(err).To(BeNil())

		err = client.Get(context.TODO(), types.NamespacedName{
			Namespace: profRhcosE8.Namespace,
			Name:      profRhcosE8.Name,
		}, profRhcosE8)
		Expect(err).To(BeNil())

		tpRhcosE8.OwnerReferences = append(tpRhcosE8.OwnerReferences,
			v1.OwnerReference{
				Name:       profRhcosE8.Name,
				Kind:       profRhcosE8.Kind,
				APIVersion: profRhcosE8.APIVersion})
		err = client.Create(context.TODO(), tpRhcosE8)
		Expect(err).To(BeNil())

		err = client.Get(context.TODO(), types.NamespacedName{
			Namespace: tpRhcosE8.Namespace,
			Name:      tpRhcosE8.Name,
		}, tpRhcosE8)
		Expect(err).To(BeNil())

		err = client.Get(context.TODO(), types.NamespacedName{
			Namespace: setting.Namespace,
			Name:      setting.Name,
		}, setting)
		Expect(err).To(BeNil())

		workerSelector = map[string]string{
			"node-role.kubernetes.io/worker": "",
		}
		masterSelector = map[string]string{
			"node-role.kubernetes.io/master": "",
		}

		reconciler = ReconcileScanSettingBinding{client: client, scheme: scheme}
	})

	Context("Creates a simple suite from a Profile", func() {
		JustBeforeEach(func() {
			ssb = &compv1alpha1.ScanSettingBinding{
				ObjectMeta: v1.ObjectMeta{
					Name:      "simple-compliance-requirements",
					Namespace: common.GetComplianceOperatorNamespace(),
				},
				Profiles: []compv1alpha1.NamedObjectReference{
					{
						Name:     profRhcosE8.Name,
						Kind:     profRhcosE8.Kind,
						APIGroup: profRhcosE8.APIVersion,
					},
				},
				SettingsRef: &compv1alpha1.NamedObjectReference{
					Name:     setting.Name,
					Kind:     setting.Kind,
					APIGroup: setting.APIVersion,
				},
			}

			ssb.Status.SetConditionPending()

			err := reconciler.client.Create(context.TODO(), ssb)
			Expect(err).To(BeNil())

			err = reconciler.client.Get(context.TODO(), types.NamespacedName{
				Namespace: ssb.Namespace,
				Name:      ssb.Name,
			}, ssb)
			Expect(err).To(BeNil())
		})

		It("Should create a basic suite from a Profile", func() {
			_, err := reconciler.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: ssb.Namespace,
					Name:      ssb.Name,
				},
			})
			Expect(err).To(BeNil())

			err = reconciler.client.Get(context.TODO(), types.NamespacedName{
				Namespace: ssb.Namespace,
				Name:      ssb.Name,
			}, ssb)
			Expect(err).To(BeNil())
			Expect(ssb.Status.Conditions.GetCondition("Ready")).ToNot(BeNil())
			Expect(ssb.Status.Conditions.IsTrueFor("Ready")).To(BeTrue())

			err = reconciler.client.Get(context.TODO(), types.NamespacedName{Name: ssb.Name, Namespace: ssb.Namespace}, suite)
			Expect(err).To(BeNil())

			Expect(suite.Spec.Schedule).To(BeEquivalentTo(setting.Schedule))
			Expect(suite.Spec.AutoApplyRemediations).To(BeTrue())

			Expect(ssb.Status.OutputRef.Name).To(Equal(suite.Name))
			Expect(*ssb.Status.OutputRef.APIGroup).To(Equal(suite.GroupVersionKind().Group))

			expScanWorker := compv1alpha1.ComplianceScanSpecWrapper{
				ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
					ScanType:           compv1alpha1.ScanTypeNode,
					ContentImage:       pBundleRhcos.Spec.ContentImage,
					Profile:            profRhcosE8.ID,
					Rule:               "",
					Content:            pBundleRhcos.Spec.ContentFile,
					NodeSelector:       workerSelector,
					TailoringConfigMap: nil,
					ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
						Debug: true,
					},
				},
				Name: profRhcosE8.Name + "-worker",
			}
			expScanMaster := compv1alpha1.ComplianceScanSpecWrapper{
				ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
					ScanType:           compv1alpha1.ScanTypeNode,
					ContentImage:       pBundleRhcos.Spec.ContentImage,
					Profile:            profRhcosE8.ID,
					Rule:               "",
					Content:            pBundleRhcos.Spec.ContentFile,
					NodeSelector:       masterSelector,
					TailoringConfigMap: nil,
					ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
						Debug: true,
					},
				},
				Name: profRhcosE8.Name + "-master",
			}
			Expect(suite.Spec.Scans).To(ConsistOf(expScanWorker, expScanMaster))
		})
	})

	Context("Creates a simple suite from a TailoredProfile", func() {
		JustBeforeEach(func() {
			ssb = &compv1alpha1.ScanSettingBinding{
				ObjectMeta: v1.ObjectMeta{
					Name:      "simple-compliance-requirements-tp",
					Namespace: common.GetComplianceOperatorNamespace(),
				},
				Profiles: []compv1alpha1.NamedObjectReference{
					{
						Name:     tpRhcosE8.Name,
						Kind:     tpRhcosE8.Kind,
						APIGroup: tpRhcosE8.APIVersion,
					},
				},
				SettingsRef: &compv1alpha1.NamedObjectReference{
					Name:     setting.Name,
					Kind:     setting.Kind,
					APIGroup: setting.APIVersion,
				},
			}
			ssb.Status.SetConditionPending()

			err := reconciler.client.Create(context.TODO(), ssb)
			Expect(err).To(BeNil())
			err = reconciler.client.Get(context.TODO(), types.NamespacedName{
				Namespace: ssb.Namespace,
				Name:      ssb.Name,
			}, ssb)
			Expect(err).To(BeNil())
		})

		It("Should create a basic suite from a TailoredProfile", func() {
			_, err := reconciler.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: ssb.Namespace,
					Name:      ssb.Name,
				},
			})
			Expect(err).To(BeNil())

			err = reconciler.client.Get(context.TODO(), types.NamespacedName{
				Namespace: ssb.Namespace,
				Name:      ssb.Name,
			}, ssb)
			Expect(err).To(BeNil())
			Expect(ssb.Status.Conditions.GetCondition("Ready")).ToNot(BeNil())
			Expect(ssb.Status.Conditions.IsTrueFor("Ready")).To(BeTrue())

			err = reconciler.client.Get(context.TODO(), types.NamespacedName{Name: ssb.Name, Namespace: ssb.Namespace}, suite)
			Expect(err).To(BeNil())

			Expect(suite.OwnerReferences).To(HaveLen(1))
			Expect(suite.OwnerReferences[0].Name).To(BeEquivalentTo(ssb.Name))
			Expect(suite.OwnerReferences[0].APIVersion).To(BeEquivalentTo(ssb.APIVersion))

			Expect(suite.Spec.Schedule).To(BeEquivalentTo(setting.Schedule))
			Expect(suite.Spec.AutoApplyRemediations).To(BeTrue())

			Expect(ssb.Status.OutputRef.Name).To(Equal(suite.Name))
			Expect(*ssb.Status.OutputRef.APIGroup).To(Equal(suite.GroupVersionKind().Group))

			expScanMaster := compv1alpha1.ComplianceScanSpecWrapper{
				ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
					ScanType:     compv1alpha1.ScanTypeNode,
					ContentImage: pBundleRhcos.Spec.ContentImage,
					Profile:      tpRhcosE8.Status.ID,
					Rule:         "",
					Content:      pBundleRhcos.Spec.ContentFile,
					NodeSelector: masterSelector,
					TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
						Name: "emptypass-rhcos4-e8-tp",
					},
					ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
						Debug: true,
					},
				},
				Name: tpRhcosE8.Name + "-master",
			}
			expScanWorker := compv1alpha1.ComplianceScanSpecWrapper{
				ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
					ScanType:     compv1alpha1.ScanTypeNode,
					ContentImage: pBundleRhcos.Spec.ContentImage,
					Profile:      tpRhcosE8.Status.ID,
					Rule:         "",
					Content:      pBundleRhcos.Spec.ContentFile,
					NodeSelector: workerSelector,
					TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
						Name: "emptypass-rhcos4-e8-tp",
					},
					ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
						Debug: true,
					},
				},
				Name: tpRhcosE8.Name + "-worker",
			}
			Expect(suite.Spec.Scans).To(ConsistOf(expScanMaster, expScanWorker))
		})
	})

	Context("Detects inconsistent products", func() {
		JustBeforeEach(func() {
			platformBadProfileAnnotations := map[string]string{
				compv1alpha1.ProductTypeAnnotation: string(compv1alpha1.ScanTypeNode),
				compv1alpha1.ProductAnnotation:     "somethingelse",
			}

			profRhcosE8Badproduct := profRhcosE8.DeepCopy()
			profRhcosE8Badproduct.SetName("e8-bad-product")
			profRhcosE8Badproduct.Annotations = platformBadProfileAnnotations
			profRhcosE8Badproduct.SetResourceVersion("")

			err := reconciler.client.Create(context.TODO(), profRhcosE8Badproduct)
			Expect(err).To(BeNil())

			ssb = &compv1alpha1.ScanSettingBinding{
				ObjectMeta: v1.ObjectMeta{
					Name:      "inconsistent-products-compliance-requirements",
					Namespace: common.GetComplianceOperatorNamespace(),
				},
				Profiles: []compv1alpha1.NamedObjectReference{
					{
						Name:     profRhcosE8Badproduct.Name,
						Kind:     profRhcosE8Badproduct.Kind,
						APIGroup: profRhcosE8Badproduct.APIVersion,
					},
					{
						Name:     profRhcosE8.Name,
						Kind:     profRhcosE8.Kind,
						APIGroup: profRhcosE8.APIVersion,
					},
				},
			}
			ssb.Status.SetConditionPending()

			err = reconciler.client.Create(context.TODO(), ssb)
			Expect(err).To(BeNil())
			err = reconciler.client.Get(context.TODO(), types.NamespacedName{
				Namespace: ssb.Namespace,
				Name:      ssb.Name,
			}, ssb)
			Expect(err).To(BeNil())
		})

		It("Should not create a suite", func() {
			_, err := reconciler.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: ssb.Namespace,
					Name:      ssb.Name,
				},
			})
			Expect(err).To(BeNil())

			err = reconciler.client.Get(context.TODO(), types.NamespacedName{
				Namespace: ssb.Namespace,
				Name:      ssb.Name,
			}, ssb)
			Expect(err).To(BeNil())
			Expect(ssb.Status.Conditions.GetCondition("Ready")).ToNot(BeNil())
			Expect(ssb.Status.Conditions.IsTrueFor("Ready")).To(BeFalse())

			err = reconciler.client.Get(context.TODO(), types.NamespacedName{Name: ssb.Name, Namespace: ssb.Namespace}, suite)
			Expect(err).ToNot(BeNil())
		})
	})

})
