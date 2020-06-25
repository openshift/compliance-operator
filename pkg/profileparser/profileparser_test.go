package profileparser

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/subchen/go-xmldom"
)

// FIXME: code duplication
func varHaveID(id string) gomegatypes.GomegaMatcher {
	return WithTransform(func(p cmpv1alpha1.Variable) string { return p.ID }, Equal(id))
}

func profHaveId(id string) gomegatypes.GomegaMatcher {
	return WithTransform(func(p cmpv1alpha1.Profile) string { return p.ID }, Equal(id))
}

func getProfileById(id string, profList []cmpv1alpha1.Profile) *cmpv1alpha1.Profile {
	for _, profile := range profList {
		if id == profile.ID {
			return &profile
		}
	}

	return nil
}

func getVariableById(id string, varList []cmpv1alpha1.Variable) *cmpv1alpha1.Variable {
	for _, variable := range varList {
		if id == variable.ID {
			return &variable
		}
	}

	return nil
}

func getRuleById(id string, ruleList []cmpv1alpha1.Rule) *cmpv1alpha1.Rule {
	for _, rule := range ruleList {
		if id == rule.ID {
			return &rule
		}
	}

	return nil
}

var (
	pcfg       *ParserConfig
	contentDom *xmldom.Document
)

func init() {
	pcfg = &ParserConfig{
		DataStreamPath: "../../tests/data/ssg-ocp4-ds-new.xml",
		ProfileBundleKey: types.NamespacedName{
			Namespace: "test-namespace",
			Name:      "test-profile",
		},
		Client: nil, // not needed for a test
		Scheme: nil, // not needed for a test
	}

	contentDom, _ = xmldom.ParseFile(pcfg.DataStreamPath)
}

var _ = Describe("Testing parse profiles", func() {
	var (
		profileList []cmpv1alpha1.Profile
	)

	BeforeEach(func() {
		// make sure init() did its job
		Expect(contentDom).NotTo(BeNil())

		profileList = make([]cmpv1alpha1.Profile, 0)
		profileAdder := func(r *cmpv1alpha1.Profile) error {
			profileList = append(profileList, *r)
			return nil
		}

		err := ParseProfilesAndDo(contentDom, pcfg, profileAdder)
		Expect(err).To(BeNil())
	})

	Context("All profiles are parsed", func() {
		const moderateId = "xccdf_org.ssgproject.content_profile_moderate"
		const platModerateId = "xccdf_org.ssgproject.content_profile_platform-moderate"
		const ncpId = "xccdf_org.ssgproject.content_profile_coreos-ncp"
		const e8Id = "xccdf_org.ssgproject.content_profile_e8"
		const ocisNode = "xccdf_org.ssgproject.content_profile_opencis-node"
		const ocisMaster = "xccdf_org.ssgproject.content_profile_opencis-master"

		It("Contains the expected variable", func() {
			Expect(profileList).Should(ConsistOf(
				profHaveId(platModerateId),
				profHaveId(ocisMaster),
				profHaveId(ocisNode),
				profHaveId(e8Id),
				profHaveId(moderateId),
				profHaveId(ncpId),
			))
		})
	})

	Context("A profile has the expected metadata", func() {
		var moderateProfile *cmpv1alpha1.Profile

		BeforeEach(func() {
			const expectedID = "xccdf_org.ssgproject.content_profile_moderate"

			moderateProfile = getProfileById(expectedID, profileList)
			Expect(moderateProfile).ToNot(BeNil())
		})

		It("Has the expected name", func() {
			const expectedName = "moderate"
			Expect(moderateProfile.Name).To(BeEquivalentTo(expectedName))
		})

		It("Has the expected title", func() {
			const expectedTitle = "NIST 800-53 Moderate-Impact Baseline for Red Hat Enterprise Linux CoreOS"
			Expect(moderateProfile.Title).To(BeEquivalentTo(expectedTitle))
		})

		It("Has some expected rules", func() {
			Expect(moderateProfile.Rules).To(ContainElements(
				cmpv1alpha1.NewProfileRule("test-profile-audit-rules-time-stime"),
				cmpv1alpha1.NewProfileRule("test-profile-sysctl-net-core-bpf-jit-harden"),
				cmpv1alpha1.NewProfileRule("test-profile-wireless-disable-in-bios"),
			))
		})

		It("Has the platform annotations", func() {
			Expect(moderateProfile.Annotations).ToNot(BeNil())
			Expect(moderateProfile.Annotations).To(HaveKeyWithValue(cmpv1alpha1.ProductTypeAnnotation, string(cmpv1alpha1.ScanTypePlatform)))
			Expect(moderateProfile.Annotations).To(HaveKeyWithValue(cmpv1alpha1.ProductAnnotation, "redhat_openshift_container_platform_4.1"))
		})
	})
})

var _ = Describe("Testing parse variables", func() {
	var (
		varList []cmpv1alpha1.Variable
	)

	BeforeEach(func() {
		// make sure init() did its job
		Expect(contentDom).NotTo(BeNil())

		varList = make([]cmpv1alpha1.Variable, 0)
		variableAdder := func(p *cmpv1alpha1.Variable) error {
			varList = append(varList, *p)
			return nil
		}

		err := ParseVariablesAndDo(contentDom, pcfg, variableAdder)
		Expect(err).To(BeNil())
	})

	Context("Some variables are parsed", func() {
		const expectedID = "xccdf_org.ssgproject.content_value_var_sshd_max_sessions"

		It("Contains the expected variable", func() {
			Expect(varList).Should(ContainElements(varHaveID(expectedID)))
		})
	})

	Context("Variables have the expected metadata", func() {
		var sshdPrivSepVar *cmpv1alpha1.Variable

		BeforeEach(func() {
			const expectedID = "xccdf_org.ssgproject.content_value_var_sshd_priv_separation"

			sshdPrivSepVar = getVariableById(expectedID, varList)
			Expect(sshdPrivSepVar).ToNot(BeNil())
		})

		It("Has the expected title", func() {
			const expectedTitle = "SSH Privilege Separation Setting"
			Expect(sshdPrivSepVar.Title).To(BeEquivalentTo(expectedTitle))
		})

		It("Has the expected description", func() {
			const expectedDescription = "Specify whether and how sshd separates privileges when handling incoming network connections."
			Expect(sshdPrivSepVar.Description).To(BeEquivalentTo(expectedDescription))
		})

		It("Has the expected selections", func() {
			Expect(sshdPrivSepVar.Selections).To(ConsistOf([]cmpv1alpha1.ValueSelection{
				{
					Description: "no",
					Value:       "no",
				},
				{
					Description: "yes",
					Value:       "yes",
				},
				{
					Description: "sandbox",
					Value:       "sandbox",
				},
			}))
		})

		It("Has the expected default value", func() {
			Expect(sshdPrivSepVar.Value).To(BeEquivalentTo("sandbox"))
		})

		It("Has the expected type", func() {
			Expect(sshdPrivSepVar.Type).To(BeEquivalentTo("string"))
		})
	})
})

var _ = Describe("Testing parse rules", func() {
	var (
		ruleList []cmpv1alpha1.Rule
	)

	BeforeEach(func() {
		// make sure init() did its job
		Expect(contentDom).NotTo(BeNil())

		ruleList = make([]cmpv1alpha1.Rule, 0)
		ruleAdder := func(r *cmpv1alpha1.Rule) error {
			ruleList = append(ruleList, *r)
			return nil
		}

		err := ParseRulesAndDo(contentDom, pcfg, ruleAdder)
		Expect(err).To(BeNil())
	})

	Context("Some rules are parsed", func() {
		const expectedID = "xccdf_org.ssgproject.content_rule_accounts_password_minlen_login_defs"
		var pwMinLenRule *cmpv1alpha1.Rule

		BeforeEach(func() {
			pwMinLenRule = getRuleById(expectedID, ruleList)
		})

		It("Contains one expected rule", func() {
			Expect(pwMinLenRule).ToNot(BeNil())
			Expect(pwMinLenRule.Annotations).ToNot(BeNil())
		})

		It("Has the expected control NIST annotations in profile operator format", func() {
			nistKey := controlAnnotationBase + "NIST-800-53"
			Expect(pwMinLenRule.Annotations).To(HaveKeyWithValue(nistKey, "IA-5(f);IA-5(1)(a);CM-6(a)"))
		})

		It("Has the expected control NIST annotations in RHACM format", func() {
			Expect(pwMinLenRule.Annotations).To(HaveKeyWithValue(rhacmStdsAnnotationKey, "NIST-800-53"))
			Expect(pwMinLenRule.Annotations).To(HaveKeyWithValue(rhacmCtrlsAnnotationsKey, "IA-5(f),IA-5(1)(a),CM-6(a)"))
		})
	})
})
