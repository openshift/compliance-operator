package profileparser

import (
	"context"
	"os"

	"github.com/antchfx/xmlquery"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage/names"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
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

func findRuleReference(profile *cmpv1alpha1.Profile, ruleName string) bool {
	for _, ruleRef := range profile.Rules {
		if string(ruleRef) == ruleName {
			return true
		}
	}

	return false
}

func doesRuleExist(cli runtimeclient.Client, namespace, ruleName string) (error, bool) {
	return doesObjectExist(cli, "Rule", namespace, ruleName)
}

func doesVariableExist(cli runtimeclient.Client, namespace, variableName string) (error, bool) {
	return doesObjectExist(cli, "Variable", namespace, variableName)
}

func doesObjectExist(cli runtimeclient.Client, kind, namespace, name string) (error, bool) {
	obj := unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   cmpv1alpha1.SchemeGroupVersion.Group,
		Version: cmpv1alpha1.SchemeGroupVersion.Version,
		Kind:    kind,
	})

	key := types.NamespacedName{Namespace: namespace, Name: name}
	err := cli.Get(context.TODO(), key, &obj)
	if errors.IsNotFound(err) {
		return nil, false
	} else if err == nil {
		return nil, true
	}

	return err, false
}

type parserInput struct {
	pcfg       *ParserConfig
	contentDom *xmlquery.Node
	pb         *cmpv1alpha1.ProfileBundle
}

func newParserInput(name, namespace, contentImage, dsPath string, client runtimeclient.Client, scheme *k8sruntime.Scheme) *parserInput {
	pi := &parserInput{
		pb: &cmpv1alpha1.ProfileBundle{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Spec: cmpv1alpha1.ProfileBundleSpec{
				ContentImage: contentImage,
			},
		},
	}

	pi.pcfg = &ParserConfig{
		DataStreamPath:   dsPath,
		ProfileBundleKey: types.NamespacedName{Name: pi.pb.Name, Namespace: pi.pb.Name},
		Client:           client,
		Scheme:           scheme,
	}

	f, _ := os.Open(pi.pcfg.DataStreamPath)
	pi.contentDom, _ = xmlquery.Parse(f)
	return pi
}

var (
	pInput         *parserInput
	pInput2        *parserInput
	pInputModified *parserInput
	client         runtimeclient.Client
)

const (
	testNamespace = "test-namespace"
)

var _ = Describe("Testing ParseBundle", func() {
	const (
		moderateProfileName        = "test-profile-moderate"
		moderateAnotherProfileName = "test-anotherprofile-moderate"

		chronydClientOnlyRuleName = "test-profile-chronyd-client-only"
		chronydNoNetworkRuleName  = "test-profile-chronyd-no-chronyc-network"
	)

	var (
		err   error
		found bool

		moderateProfilePre  *cmpv1alpha1.Profile
		moderateProfilePost *cmpv1alpha1.Profile
	)

	BeforeEach(func() {
		err = ParseBundle(pInput.contentDom, pInput.pb, pInput.pcfg)
		Expect(err).To(BeNil())

		moderateProfilePre = &cmpv1alpha1.Profile{}
		key := types.NamespacedName{Namespace: testNamespace, Name: moderateProfileName}
		err = client.Get(context.TODO(), key, moderateProfilePre)
		Expect(err).To(BeNil())

		err = ParseBundle(pInput2.contentDom, pInput2.pb, pInput2.pcfg)
		Expect(err).To(BeNil())

		moderateAnotherProfile := &cmpv1alpha1.Profile{}
		key = types.NamespacedName{Namespace: testNamespace, Name: moderateAnotherProfileName}
		err = client.Get(context.TODO(), key, moderateAnotherProfile)
		Expect(err).To(BeNil())

		// Check that expected data is found
		err, found = doesRuleExist(client, testNamespace, chronydClientOnlyRuleName)
		Expect(err).To(BeNil())
		Expect(found).To(BeTrue())

		found = findRuleReference(moderateProfilePre, chronydNoNetworkRuleName)
		Expect(found).To(BeTrue())
	})

	JustBeforeEach(func() {
		// Parse the modified profile so that tests can be targeted to a single case
		err = ParseBundle(pInputModified.contentDom, pInputModified.pb, pInputModified.pcfg)
		Expect(err).To(BeNil())

		moderateProfilePost = &cmpv1alpha1.Profile{}
		key := types.NamespacedName{Namespace: testNamespace, Name: moderateProfileName}
		err = client.Get(context.TODO(), key, moderateProfilePost)
		Expect(err).To(BeNil())
	})

	Context("Profile changes", func() {
		const (
			ncpProfileName         = "test-profile-coreos-ncp"
			ncpModifiedProfileName = "test-profile-coreos-ncp-modified"
			e8ProfileName          = "test-profile-e8"
		)

		var (
			ncpProfile    *cmpv1alpha1.Profile
			e8ProfilePre  *cmpv1alpha1.Profile
			e8ProfilePost *cmpv1alpha1.Profile
		)

		BeforeEach(func() {
			ncpProfile = &cmpv1alpha1.Profile{}
			key := types.NamespacedName{Namespace: testNamespace, Name: ncpProfileName}
			err = client.Get(context.TODO(), key, ncpProfile)
			Expect(err).To(BeNil())

			e8ProfilePre = &cmpv1alpha1.Profile{}
			key = types.NamespacedName{Namespace: testNamespace, Name: e8ProfileName}
			err = client.Get(context.TODO(), key, e8ProfilePre)
			Expect(err).To(BeNil())

			err, found = doesRuleExist(client, testNamespace, chronydNoNetworkRuleName)
			Expect(err).To(BeNil())
			Expect(found).To(BeTrue())

			found = findRuleReference(moderateProfilePre, chronydClientOnlyRuleName)
			Expect(found).To(BeTrue())
		})

		JustBeforeEach(func() {
			e8ProfilePost = &cmpv1alpha1.Profile{}
			key := types.NamespacedName{Namespace: testNamespace, Name: e8ProfileName}
			err = client.Get(context.TODO(), key, e8ProfilePost)
			Expect(err).To(BeNil())

			// Make sure that the other profile is still there
			moderateAnotherProfile := &cmpv1alpha1.Profile{}
			key = types.NamespacedName{Namespace: testNamespace, Name: moderateAnotherProfileName}
			err = client.Get(context.TODO(), key, moderateAnotherProfile)
			Expect(err).To(BeNil())
		})

		It("Detects that a profile was removed completely", func() {
			ncpProfile = &cmpv1alpha1.Profile{}
			key := types.NamespacedName{Namespace: testNamespace, Name: ncpProfileName}
			err = client.Get(context.TODO(), key, ncpProfile)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("Detects that a new profile was added", func() {
			ncpModifiedProfile := &cmpv1alpha1.Profile{}
			key := types.NamespacedName{Namespace: testNamespace, Name: ncpModifiedProfileName}
			err = client.Get(context.TODO(), key, ncpModifiedProfile)
			Expect(err).To(BeNil())
		})

		It("Detects that a rule was unlinked from a profile", func() {
			// The rule must still exist
			err, found = doesRuleExist(client, testNamespace, chronydClientOnlyRuleName)
			Expect(err).To(BeNil())
			Expect(found).To(BeTrue())

			// But not be part of the profile
			found := findRuleReference(moderateProfilePost, chronydClientOnlyRuleName)
			Expect(found).To(BeFalse())
		})

		It("Detects that a rule was linked to a profile", func() {
			// This rule was not part of the e8 profile before
			found := findRuleReference(e8ProfilePre, chronydClientOnlyRuleName)
			Expect(found).To(BeFalse())

			// The updated e8 profile should have the rule
			found = findRuleReference(e8ProfilePost, chronydClientOnlyRuleName)
			Expect(found).To(BeTrue())
		})
	})

	Context("Rule changes", func() {
		const (
			foobarRuleName         = "test-profile-service-foobar-enabled"
			chronydMaxpollRuleName = "test-profile-chronyd-or-ntpd-set-maxpoll"
		)

		var (
			ruleChangedSeverityPre *cmpv1alpha1.Rule
		)

		BeforeEach(func() {
			ruleChangedSeverityPre = &cmpv1alpha1.Rule{}
			key := types.NamespacedName{Namespace: testNamespace, Name: chronydMaxpollRuleName}
			err := client.Get(context.TODO(), key, ruleChangedSeverityPre)
			Expect(err).To(BeNil())

			// This rule does not exist in the old profile
			err, found = doesRuleExist(client, testNamespace, foobarRuleName)
			Expect(err).To(BeNil())
			Expect(found).To(BeFalse())
		})

		It("Detects that a rule was added", func() {
			err, found = doesRuleExist(client, testNamespace, foobarRuleName)
			Expect(err).To(BeNil())
			Expect(found).To(BeTrue())
		})

		It("Detects that a rule was removed completely", func() {
			// The rule neither exists
			err, found = doesRuleExist(client, testNamespace, chronydNoNetworkRuleName)
			Expect(err).To(BeNil())
			Expect(found).To(BeFalse())

			// Nor is it part of a profile
			found := findRuleReference(moderateProfilePost, chronydNoNetworkRuleName)
			Expect(found).To(BeFalse())
		})

		It("Did not change more than expected", func() {
			// One rule was unlinked, one was removed
			Expect(moderateProfilePre.Rules).To(HaveLen(len(moderateProfilePost.Rules) + 2))
		})

		It("Detects that a rule has changed severity", func() {
			ruleChangedSeverityPost := &cmpv1alpha1.Rule{}
			key := types.NamespacedName{Namespace: testNamespace, Name: chronydMaxpollRuleName}
			err := client.Get(context.TODO(), key, ruleChangedSeverityPost)
			Expect(err).To(BeNil())

			Expect(ruleChangedSeverityPost.Severity).ToNot(BeEquivalentTo(ruleChangedSeverityPre.Severity))
		})
	})

	Context("Variable changes", func() {
		const (
			accountDisablePwExpiryVarName = "test-profile-var-account-disable-post-pw-expiration"
			accountsPassMinLenVarName     = "test-profile-var-accounts-password-minlen-login-defs"
			foobarVariableName            = "test-profile-var-foobar"
		)

		var (
			accountsPassMinLenVarPre *cmpv1alpha1.Variable
		)

		BeforeEach(func() {
			accountsPassMinLenVarPre = &cmpv1alpha1.Variable{}
			key := types.NamespacedName{Namespace: testNamespace, Name: accountsPassMinLenVarName}
			err = client.Get(context.TODO(), key, accountsPassMinLenVarPre)
			Expect(err).To(BeNil())

			// This variable does not exist in the old profile
			err, found = doesVariableExist(client, testNamespace, foobarVariableName)
			Expect(err).To(BeNil())
			Expect(found).To(BeFalse())

			err, found = doesVariableExist(client, testNamespace, accountDisablePwExpiryVarName)
			Expect(err).To(BeNil())
			Expect(found).To(BeTrue())
		})

		It("Detects that a variable was added", func() {
			err, found = doesVariableExist(client, testNamespace, foobarVariableName)
			Expect(err).To(BeNil())
			Expect(found).To(BeTrue())
		})

		It("Detects that a variable was removed completely", func() {
			// The rule neither exists
			err, found = doesVariableExist(client, testNamespace, accountDisablePwExpiryVarName)
			Expect(err).To(BeNil())
			Expect(found).To(BeFalse())
		})

		It("Detects that a variable has changed values", func() {
			accountsPassMinLenVar := &cmpv1alpha1.Variable{}
			key := types.NamespacedName{Namespace: testNamespace, Name: accountsPassMinLenVarName}
			err := client.Get(context.TODO(), key, accountsPassMinLenVar)
			Expect(err).To(BeNil())

			Expect(accountsPassMinLenVar.Value).ToNot(BeEquivalentTo(accountsPassMinLenVarPre.Value))
		})
	})
})

var _ = Describe("Testing parse profiles", func() {
	var (
		profileList []cmpv1alpha1.Profile
		profchan    chan cmpv1alpha1.Profile
	)

	BeforeEach(func() {
		// make sure init() did its job
		Expect(pInput.contentDom).NotTo(BeNil())
		profchan = make(chan cmpv1alpha1.Profile, 500)
		done := make(chan bool)

		profileList = make([]cmpv1alpha1.Profile, 0)
		profileAdder := func(r *cmpv1alpha1.Profile) error {
			profchan <- *r
			return nil
		}

		go func() {
			for p := range profchan {
				profileList = append(profileList, p)
			}
			close(done)
		}()

		nonce := names.SimpleNameGenerator.GenerateName("pb-")
		err := ParseProfilesAndDo(pInput.contentDom, pInput.pb, nonce, profileAdder)
		Expect(err).To(BeNil())
		close(profchan)
		<-done
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
		varchan chan cmpv1alpha1.Variable
	)

	BeforeEach(func() {
		// make sure init() did its job
		Expect(pInput.contentDom).NotTo(BeNil())
		varchan = make(chan cmpv1alpha1.Variable, 1000)
		done := make(chan bool)

		varList = make([]cmpv1alpha1.Variable, 0)
		variableAdder := func(p *cmpv1alpha1.Variable) error {
			varchan <- *p
			return nil
		}

		go func() {
			for v := range varchan {

				varList = append(varList, v)
			}
			close(done)
		}()

		nonce := names.SimpleNameGenerator.GenerateName("pb-")
		err := ParseVariablesAndDo(pInput.contentDom, pInput.pb, nonce, variableAdder)
		Expect(err).To(BeNil())
		close(varchan)
		<-done
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
		rchan    chan cmpv1alpha1.Rule
	)

	BeforeEach(func() {
		// make sure init() did its job
		Expect(pInput.contentDom).NotTo(BeNil())
		rchan = make(chan cmpv1alpha1.Rule, 1000)
		done := make(chan bool)

		ruleList = make([]cmpv1alpha1.Rule, 100)
		ruleAdder := func(r *cmpv1alpha1.Rule) error {
			rchan <- *r
			return nil
		}

		go func() {
			for r := range rchan {
				ruleList = append(ruleList, r)
			}
			close(done)
		}()

		stdParser := newStandardParser()
		nonce := names.SimpleNameGenerator.GenerateName("pb-")
		zaplog, _ := zap.NewDevelopment()
		log = zapr.NewLogger(zaplog)

		err := ParseRulesAndDo(pInput.contentDom, stdParser, pInput.pb, nonce, ruleAdder)
		Expect(err).To(BeNil())

		close(rchan)
		<-done
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

		It("Has the expected severity", func() {
			Expect(pwMinLenRule.Severity).To(BeEquivalentTo("medium"))
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

var _ = Describe("Testing CPE string parsing in isolation", func() {
	Context("Malformed CPE string", func() {
		It("Returns the defaults if the CPE uri does not start with cpe://", func() {
			pType, pName := parseProductTypeAndName("xxx:/a:redhat:enterprise_linux_coreos:4", cmpv1alpha1.ScanTypeNode, "default")
			Expect(pType).To(BeEquivalentTo(cmpv1alpha1.ScanTypeNode))
			Expect(pName).To(BeEquivalentTo("default"))
		})

		It("Returns the defaults if the CPE uri is too short", func() {
			pType, pName := parseProductTypeAndName("cpe:/", cmpv1alpha1.ScanTypeNode, "default")
			Expect(pType).To(BeEquivalentTo(cmpv1alpha1.ScanTypeNode))
			Expect(pName).To(BeEquivalentTo("default"))
		})

		It("Does not crash with with a CPE string without platform information", func() {
			pType, pName := parseProductTypeAndName("cpe:/a:", cmpv1alpha1.ScanTypeNode, "unusedDefault")
			Expect(pType).To(BeEquivalentTo(cmpv1alpha1.ScanTypePlatform))
			Expect(pName).To(BeEquivalentTo(""))
		})
	})
})

var _ = Describe("Performance", func() {
	BeforeEach(func() {
		Expect(pInput.contentDom).NotTo(BeNil())
	})

	Context("Testing parsing profiles", func() {
		Measure("it should parse profiles efficiently", func(b Benchmarker) {
			rtime := b.Time("runtime", func() {
				err := ParseBundle(pInput.contentDom, pInput.pb, pInput.pcfg)
				Expect(err).To(BeNil())
			})

			Ω(rtime.Seconds()).Should(BeNumerically("<", 1.2), "ParseProfilesAndDo() shouldn't take too long.")
		}, 1000)

	})
})
