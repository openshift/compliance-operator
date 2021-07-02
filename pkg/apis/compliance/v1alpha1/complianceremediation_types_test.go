package v1alpha1

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Testing ComplianceRemediation API", func() {
	var rem *ComplianceRemediation
	When("parsing dependency references", func() {
		BeforeEach(func() {
			rem = &ComplianceRemediation{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			}

		})
		It("parses a cluster-scoped resource correctly", func() {
			rem.Annotations[RemediationObjectDependencyAnnotation] = `[{"apiVersion":"templates.gatekeeper.sh/v1beta1","kind":"ConstraintTemplate","name":"etcdencryptedonly"}]`
			deps, err := rem.ParseRemediationDependencyRefs()
			Expect(err).To(BeNil())
			Expect(deps).To(HaveLen(1))
			Expect(deps[0].APIVersion).To(Equal("templates.gatekeeper.sh/v1beta1"))
			Expect(deps[0].Kind).To(Equal("ConstraintTemplate"))
			Expect(deps[0].Name).To(Equal("etcdencryptedonly"))
		})
		It("parses a namespaced-scoped resource correctly", func() {
			rem.Annotations[RemediationObjectDependencyAnnotation] = `[{"apiVersion":"v1","kind":"Secret","name":"foo","namespace":"bar"}]`
			deps, err := rem.ParseRemediationDependencyRefs()
			Expect(err).To(BeNil())
			Expect(deps).To(HaveLen(1))
			Expect(deps[0].APIVersion).To(Equal("v1"))
			Expect(deps[0].Kind).To(Equal("Secret"))
			Expect(deps[0].Name).To(Equal("foo"))
			Expect(deps[0].Namespace).To(Equal("bar"))
		})
		It("returns an empty list if dependency annotation is empty string", func() {
			rem.Annotations[RemediationObjectDependencyAnnotation] = ""
			deps, err := rem.ParseRemediationDependencyRefs()
			Expect(err).To(BeNil())
			Expect(deps).To(HaveLen(0))
		})
		It("returns an error if json is malformed", func() {
			rem.Annotations[RemediationObjectDependencyAnnotation] = `[{"apiVersion":"v1","kind":"Secret","name":"foo","namespace":"bar"]`
			_, err := rem.ParseRemediationDependencyRefs()
			Expect(err).ToNot(BeNil())
		})
		It("returns an error if no annotation is set", func() {
			_, err := rem.ParseRemediationDependencyRefs()
			Expect(err).ToNot(BeNil())
			Expect(err).To(MatchError(KubeDepsNotFound))
		})
	})
})
