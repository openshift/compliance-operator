package tailoredprofile

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/compliance-operator/pkg/apis"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

var _ = Describe("TailoredprofileController", func() {

	var (
		ctx         = context.Background()
		namespace   = "test-ns"
		profileName = "my-profile"
		r           *ReconcileTailoredProfile
	)

	BeforeEach(func() {
		cscheme := scheme.Scheme
		err := apis.AddToScheme(cscheme)
		Expect(err).To(BeNil())

		pb1 := &compv1alpha1.ProfileBundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pb-1",
				Namespace: namespace,
			},
		}
		pb2 := &compv1alpha1.ProfileBundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pb-2",
				Namespace: namespace,
			},
		}
		p := &compv1alpha1.Profile{
			ObjectMeta: metav1.ObjectMeta{
				Name:      profileName,
				Namespace: namespace,
			},
			ProfilePayload: compv1alpha1.ProfilePayload{
				ID: "profile_1",
				Rules: []compv1alpha1.ProfileRule{
					"rule-1",
					"rule-2",
				},
				Values: []compv1alpha1.ProfileValue{
					"key1=val1",
					"key2=val2",
				},
			},
		}
		crefErr := controllerutil.SetControllerReference(pb1, p, cscheme)
		Expect(crefErr).To(BeNil())

		objs := []runtime.Object{pb1.DeepCopy(), pb2.DeepCopy(), p.DeepCopy()}

		for i := 1; i < 7; i++ {
			r := &compv1alpha1.Rule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("rule-%d", i),
					Namespace: namespace,
				},
				RulePayload: compv1alpha1.RulePayload{
					ID: fmt.Sprintf("rule_%d", i),
				},
			}
			v := &compv1alpha1.Variable{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("var-%d", i),
					Namespace: namespace,
				},
				VariablePayload: compv1alpha1.VariablePayload{
					ID: fmt.Sprintf("var_%d", i),
				},
			}

			// Rules and Variables 1, 2, 3, 4 are owned by pb1
			if i < 5 {
				crefErr := controllerutil.SetControllerReference(pb1, r, cscheme)
				Expect(crefErr).To(BeNil())
				crefErr = controllerutil.SetControllerReference(pb1, v, cscheme)
				Expect(crefErr).To(BeNil())
			} else {
				crefErr := controllerutil.SetControllerReference(pb2, r, cscheme)
				Expect(crefErr).To(BeNil())
				crefErr = controllerutil.SetControllerReference(pb2, v, cscheme)
				Expect(crefErr).To(BeNil())
			}
			objs = append(objs, r.DeepCopy(), v.DeepCopy())
		}

		client := fake.NewFakeClientWithScheme(cscheme, objs...)
		r = &ReconcileTailoredProfile{client: client, scheme: cscheme}
	})

	When("extending a profile", func() {
		var tpName = "tailoring"
		BeforeEach(func() {
			tp := &compv1alpha1.TailoredProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tpName,
					Namespace: namespace,
				},
				Spec: compv1alpha1.TailoredProfileSpec{
					Extends: profileName,
					EnableRules: []compv1alpha1.RuleReferenceSpec{
						{
							Name:      "rule-3",
							Rationale: "Why not",
						},
					},
					DisableRules: []compv1alpha1.RuleReferenceSpec{
						{
							Name:      "rule-2",
							Rationale: "Why not",
						},
					},
				},
			}

			createErr := r.client.Create(ctx, tp)
			Expect(createErr).To(BeNil())
		})
		It("successfully creates a profile with an extra rule", func() {
			tpKey := types.NamespacedName{
				Name:      tpName,
				Namespace: namespace,
			}
			tpReq := reconcile.Request{}
			tpReq.Name = tpName
			tpReq.Namespace = namespace

			By("Reconciling the first time")
			_, err := r.Reconcile(tpReq)
			Expect(err).To(BeNil())

			tp := &compv1alpha1.TailoredProfile{}
			geterr := r.client.Get(ctx, tpKey, tp)
			Expect(geterr).To(BeNil())

			By("Sets the extended profile as the owner")
			ownerRefs := tp.GetOwnerReferences()
			Expect(ownerRefs).To(HaveLen(1))
			Expect(ownerRefs[0].Name).To(Equal(profileName))
			Expect(ownerRefs[0].Kind).To(Equal("Profile"))

			By("Reconciling a second time")
			_, err = r.Reconcile(tpReq)

			geterr = r.client.Get(ctx, tpKey, tp)
			Expect(geterr).To(BeNil())

			By("Has the appropriate status")
			Expect(tp.Status.State).To(Equal(compv1alpha1.TailoredProfileStateReady))
			Expect(tp.Status.OutputRef.Name).To(Equal(tp.Name + "-tp"))
			Expect(tp.Status.OutputRef.Namespace).To(Equal(tp.Namespace))

			By("Generated an appropriate ConfigMap")
			cm := &corev1.ConfigMap{}
			cmKey := types.NamespacedName{
				Name:      tp.Status.OutputRef.Name,
				Namespace: tp.Status.OutputRef.Namespace,
			}

			geterr = r.client.Get(ctx, cmKey, cm)
			Expect(geterr).To(BeNil())
			data := cm.Data["tailoring.xml"]
			Expect(data).To(ContainSubstring(`extends="profile_1"`))
			Expect(data).To(ContainSubstring(`select idref="rule_3" selected="true"`))
			Expect(data).To(ContainSubstring(`select idref="rule_2" selected="false"`))
		})
	})

	When("extending a profile with reference to another bundle", func() {
		var tpName = "tailoring"
		Context("with a rule from another bundle", func() {
			BeforeEach(func() {
				tp := &compv1alpha1.TailoredProfile{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tpName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.TailoredProfileSpec{
						Extends: profileName,
						EnableRules: []compv1alpha1.RuleReferenceSpec{
							{
								Name:      "rule-5",
								Rationale: "Why not",
							},
						},
					},
				}

				createErr := r.client.Create(ctx, tp)
				Expect(createErr).To(BeNil())
			})
			It("reports an error", func() {
				tpKey := types.NamespacedName{
					Name:      tpName,
					Namespace: namespace,
				}
				tpReq := reconcile.Request{}
				tpReq.Name = tpName
				tpReq.Namespace = namespace

				By("Reconciling the first time")
				_, err := r.Reconcile(tpReq)
				Expect(err).To(BeNil())

				tp := &compv1alpha1.TailoredProfile{}
				geterr := r.client.Get(ctx, tpKey, tp)
				Expect(geterr).To(BeNil())

				By("Sets the profile as the owner")
				ownerRefs := tp.GetOwnerReferences()
				Expect(ownerRefs).To(HaveLen(1))
				Expect(ownerRefs[0].Name).To(Equal(profileName))
				Expect(ownerRefs[0].Kind).To(Equal("Profile"))

				By("Reconciling a second time")
				_, err = r.Reconcile(tpReq)

				geterr = r.client.Get(ctx, tpKey, tp)
				Expect(geterr).To(BeNil())

				By("Has the appropriate error status")
				Expect(tp.Status.State).To(Equal(compv1alpha1.TailoredProfileStateError))
				Expect(tp.Status.ErrorMessage).To(MatchRegexp(
					`rule .* not owned by expected ProfileBundle .*`))
			})
		})

		Context("with a variable from another bundle", func() {
			BeforeEach(func() {
				tp := &compv1alpha1.TailoredProfile{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tpName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.TailoredProfileSpec{
						Extends: profileName,
						SetValues: []compv1alpha1.VariableValueSpec{
							{
								Name:      "var-5",
								Rationale: "Why not",
								Value:     "1234",
							},
						},
					},
				}

				createErr := r.client.Create(ctx, tp)
				Expect(createErr).To(BeNil())
			})
			It("reports an error", func() {
				tpKey := types.NamespacedName{
					Name:      tpName,
					Namespace: namespace,
				}
				tpReq := reconcile.Request{}
				tpReq.Name = tpName
				tpReq.Namespace = namespace

				By("Reconciling the first time")
				_, err := r.Reconcile(tpReq)
				Expect(err).To(BeNil())

				tp := &compv1alpha1.TailoredProfile{}
				geterr := r.client.Get(ctx, tpKey, tp)
				Expect(geterr).To(BeNil())

				By("Sets the profile as the owner")
				ownerRefs := tp.GetOwnerReferences()
				Expect(ownerRefs).To(HaveLen(1))
				Expect(ownerRefs[0].Name).To(Equal(profileName))
				Expect(ownerRefs[0].Kind).To(Equal("Profile"))

				By("Reconciling a second time")
				_, err = r.Reconcile(tpReq)

				geterr = r.client.Get(ctx, tpKey, tp)
				Expect(geterr).To(BeNil())

				By("Has the appropriate error status")
				Expect(tp.Status.State).To(Equal(compv1alpha1.TailoredProfileStateError))
				Expect(tp.Status.ErrorMessage).To(MatchRegexp(
					`variable .* not owned by expected ProfileBundle .*`))
			})
		})
	})

	When("Trying to reference an unexistent rule", func() {
		var tpName = "tailoring"
		BeforeEach(func() {
			tp := &compv1alpha1.TailoredProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tpName,
					Namespace: namespace,
				},
				Spec: compv1alpha1.TailoredProfileSpec{
					Extends: profileName,
					EnableRules: []compv1alpha1.RuleReferenceSpec{
						{
							Name: "unexistent",
						},
					},
				},
			}

			createErr := r.client.Create(ctx, tp)
			Expect(createErr).To(BeNil())
		})
		It("reports an error", func() {
			tpKey := types.NamespacedName{
				Name:      tpName,
				Namespace: namespace,
			}
			tpReq := reconcile.Request{}
			tpReq.Name = tpName
			tpReq.Namespace = namespace

			By("Reconciling the first time")
			_, err := r.Reconcile(tpReq)
			Expect(err).To(BeNil())

			By("Reconciling a second time")
			_, err = r.Reconcile(tpReq)

			tp := &compv1alpha1.TailoredProfile{}
			geterr := r.client.Get(ctx, tpKey, tp)
			Expect(geterr).To(BeNil())

			By("Has the appropriate error status")
			Expect(tp.Status.State).To(Equal(compv1alpha1.TailoredProfileStateError))
			Expect(tp.Status.ErrorMessage).To(MatchRegexp(
				`not found`))
		})
	})

	When("Trying to reference an unexistent variable", func() {
		var tpName = "tailoring"
		BeforeEach(func() {
			tp := &compv1alpha1.TailoredProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tpName,
					Namespace: namespace,
				},
				Spec: compv1alpha1.TailoredProfileSpec{
					Extends: profileName,
					SetValues: []compv1alpha1.VariableValueSpec{
						{
							Name: "unexistent",
						},
					},
				},
			}

			createErr := r.client.Create(ctx, tp)
			Expect(createErr).To(BeNil())
		})
		It("reports an error", func() {
			tpKey := types.NamespacedName{
				Name:      tpName,
				Namespace: namespace,
			}
			tpReq := reconcile.Request{}
			tpReq.Name = tpName
			tpReq.Namespace = namespace

			By("Reconciling the first time")
			_, err := r.Reconcile(tpReq)
			Expect(err).To(BeNil())

			By("Reconciling a second time")
			_, err = r.Reconcile(tpReq)

			tp := &compv1alpha1.TailoredProfile{}
			geterr := r.client.Get(ctx, tpKey, tp)
			Expect(geterr).To(BeNil())

			By("Has the appropriate error status")
			Expect(tp.Status.State).To(Equal(compv1alpha1.TailoredProfileStateError))
			Expect(tp.Status.ErrorMessage).To(MatchRegexp(
				`not found`))
		})
	})
})
