package xccdf

import (
	"strings"

	"github.com/antchfx/xmlquery"
	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type tailoredValue struct {
	ID    string
	Value string
}

func findVariablesInTailoring(tailoring string) ([]tailoredValue, error) {
	tailoringDom, err := xmlquery.Parse(strings.NewReader(tailoring))
	if err != nil {
		return nil, err
	}

	tailoredVars := []tailoredValue{}
	tailoringVarNodes := tailoringDom.SelectElements("//xccdf-1.2:set-value")
	for _, tVarNode := range tailoringVarNodes {
		tailoredVars = append(tailoredVars, tailoredValue{
			ID:    tVarNode.SelectAttr("idref"),
			Value: tVarNode.InnerText(),
		})
	}

	return tailoredVars, nil
}

var _ = Describe("Testing parse variables", func() {
	var (
		tp        *cmpv1alpha1.TailoredProfile
		p         *cmpv1alpha1.Profile
		pb        *cmpv1alpha1.ProfileBundle
		variables []*cmpv1alpha1.Variable
		tailoring string
		err       error
	)

	BeforeEach(func() {
		p = &cmpv1alpha1.Profile{
			ProfilePayload: cmpv1alpha1.ProfilePayload{
				ID: "profile_id",
			},
		}

		pb = &cmpv1alpha1.ProfileBundle{Spec: cmpv1alpha1.ProfileBundleSpec{ContentFile: "/path/to/a/file/"}}

		tp = &cmpv1alpha1.TailoredProfile{
			TypeMeta: v1.TypeMeta{
				Kind:       "TailoredProfile",
				APIVersion: cmpv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: v1.ObjectMeta{
				Name:      "tailoredProfileName",
				Namespace: "tailoredProfileNamespace",
			},
			Spec:   cmpv1alpha1.TailoredProfileSpec{},
			Status: cmpv1alpha1.TailoredProfileStatus{},
		}

	})

	Context("tailoring variables", func() {
		BeforeEach(func() {
			variables = []*cmpv1alpha1.Variable{
				{
					VariablePayload: cmpv1alpha1.VariablePayload{
						ID:    "foo_id",
						Type:  "string",
						Value: "fooval",
					},
				},
				{
					VariablePayload: cmpv1alpha1.VariablePayload{
						ID:    "bar_id",
						Type:  "int",
						Value: "3",
					},
				},
				{
					VariablePayload: cmpv1alpha1.VariablePayload{
						ID:    "baz_id",
						Type:  "bool",
						Value: "true",
					},
				},
			}
		})

		JustBeforeEach(func() {
			tailoring, err = TailoredProfileToXML(tp, p, pb, nil, variables)
			Expect(err).To(BeNil())
		})

		It("renders the variables", func() {
			tailoredVars, err := findVariablesInTailoring(tailoring)
			Expect(err).To(BeNil())
			Expect(tailoredVars).To(HaveLen(len(variables)))
			Expect(tailoredVars).To(ConsistOf(
				tailoredValue{ID: "foo_id", Value: "fooval"},
				tailoredValue{ID: "bar_id", Value: "3"},
				tailoredValue{ID: "baz_id", Value: "true"}))
		})
	})
})
