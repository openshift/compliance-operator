package profileparser

import (
	"testing"

	compapis "github.com/openshift/compliance-operator/pkg/apis"
	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = BeforeSuite(func() {
	objs := []k8sruntime.Object{}
	objs = append(objs, &cmpv1alpha1.ProfileBundle{}, &cmpv1alpha1.Profile{}, &cmpv1alpha1.ProfileList{})

	cmpScheme := k8sruntime.NewScheme()
	_ = compapis.AddToScheme(cmpScheme)
	client = fake.NewFakeClientWithScheme(cmpScheme)

	pInput = newParserInput("test-profile", testNamespace,
		fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_baseline"),
		"../../tests/data/ssg-ocp4-ds-new.xml",
		client, cmpScheme)

	pInput2 = newParserInput("test-anotherprofile", testNamespace,
		fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_baseline"),
		"../../tests/data/ssg-ocp4-ds-new.xml",
		client, cmpScheme)

	pInputModified = newParserInput("test-profile", testNamespace,
		fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_mod"),
		"../../tests/data/ssg-ocp4-ds-new-modified.xml",
		client, cmpScheme)
})

func TestUtils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Profileparser Suite")
}
