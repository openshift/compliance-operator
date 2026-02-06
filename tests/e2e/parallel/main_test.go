package parallel_e2e

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"testing"

	compv1alpha1 "github.com/ComplianceAsCode/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/ComplianceAsCode/compliance-operator/tests/e2e/framework"
	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var brokenContentImagePath string
var contentImagePath string

func TestMain(m *testing.M) {
	f := framework.NewFramework()
	err := f.SetUp()
	if err != nil {
		log.Fatal(err)
	}

	contentImagePath = os.Getenv("CONTENT_IMAGE")
	if contentImagePath == "" {
		fmt.Println("Please set the 'CONTENT_IMAGE' environment variable")
		os.Exit(1)
	}

	brokenContentImagePath = os.Getenv("BROKEN_CONTENT_IMAGE")

	if brokenContentImagePath == "" {
		fmt.Println("Please set the 'BROKEN_CONTENT_IMAGE' environment variable")
		os.Exit(1)
	}

	exitCode := m.Run()
	if exitCode == 0 || (exitCode > 0 && f.CleanUpOnError()) {
		if err = f.TearDown(); err != nil {
			log.Fatal(err)
		}
	}
	os.Exit(exitCode)
}

func TestProfileVersion(t *testing.T) {
	t.Parallel()
	f := framework.Global

	profile := &compv1alpha1.Profile{}
	// We know this profile has a version and it's set in the ComplianceAsCode/content
	profileName := "ocp4-cis"
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: f.OperatorNamespace, Name: profileName}, profile); err != nil {
		t.Fatalf("failed to get profile %s: %s", profileName, err)
	}
	if profile.Version == "" {
		t.Fatalf("expected profile %s to have version set", profileName)
	}
}

func TestProfileModification(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		removedRule         = "chronyd-no-chronyc-network"
		unlinkedRule        = "chronyd-client-only"
		moderateProfileName = "moderate"
	)
	var (
		baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_baseline")
		modifiedImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_mod")
	)

	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	pbName := framework.GetObjNameFromTest(t)
	origPb, err := f.CreateProfileBundle(pbName, baselineImage, framework.RhcosContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle: %s", err)
	}
	// This should get cleaned up at the end of the test
	defer f.Client.Delete(context.TODO(), origPb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for the ProfileBundle to become available: %s", err)
	}
	if err := f.AssertMustHaveParsedProfiles(pbName, string(compv1alpha1.ScanTypeNode), "redhat_enterprise_linux_coreos_4"); err != nil {
		t.Fatalf("failed checking profiles in ProfileBundle: %s", err)
	}

	// Check that the rule we removed exists in the original profile
	removedRuleName := prefixName(pbName, removedRule)
	err, found := f.DoesRuleExist(origPb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found != true {
		t.Fatalf("expected rule %s to exist in namespace %s", removedRuleName, origPb.Namespace)
	}

	// Check that the rule we unlined in the modified profile is linked in the original
	profileName := prefixName(pbName, moderateProfileName)
	profilePreUpdate := &compv1alpha1.Profile{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: origPb.Namespace, Name: profileName}, profilePreUpdate); err != nil {
		t.Fatalf("failed to get profile %s", profileName)
	}
	unlinkedRuleName := prefixName(pbName, unlinkedRule)
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePreUpdate)
	if found == false {
		t.Fatalf("failed to find rule %s in profile %s", unlinkedRule, profileName)
	}

	// update the image with a new hash
	modPb := origPb.DeepCopy()
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: modPb.Namespace, Name: modPb.Name}, modPb); err != nil {
		t.Fatalf("failed to get ProfileBundle %s", modPb.Name)
	}

	modPb.Spec.ContentImage = modifiedImage
	if err := f.Client.Update(context.TODO(), modPb); err != nil {
		t.Fatalf("failed to update ProfileBundle %s: %s", modPb.Name, err)
	}

	// Wait for the update to happen, the PB will flip first to pending, then to valid
	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed to parse ProfileBundle %s: %s", pbName, err)
	}

	if err := f.AssertProfileBundleMustHaveParsedRules(pbName); err != nil {
		t.Fatal(err)
	}

	// We removed this rule in the update, is must no longer exist
	err, found = f.DoesRuleExist(origPb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatalf("rule %s unexpectedly found", removedRuleName)
	}

	// This rule was unlinked
	profilePostUpdate := &compv1alpha1.Profile{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: origPb.Namespace, Name: profileName}, profilePostUpdate); err != nil {
		t.Fatalf("failed to get profile %s: %s", profileName, err)
	}
	framework.IsRuleInProfile(unlinkedRuleName, profilePostUpdate)
	if found {
		t.Fatalf("rule %s unexpectedly found", unlinkedRuleName)
	}
}

func TestProfileISTagUpdate(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		removedRule         = "chronyd-no-chronyc-network"
		unlinkedRule        = "chronyd-client-only"
		moderateProfileName = "moderate"
	)
	var (
		baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_baseline")
		modifiedImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_mod")
	)

	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	pbName := framework.GetObjNameFromTest(t)
	iSName := pbName

	s, err := f.CreateImageStream(iSName, f.OperatorNamespace, baselineImage)
	if err != nil {
		t.Fatalf("failed to create image stream %s", iSName)
	}
	defer f.Client.Delete(context.TODO(), s)

	baselineImage = fmt.Sprintf("%s:%s", iSName, "latest")
	pb, err := f.CreateProfileBundle(pbName, baselineImage, framework.RhcosContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle: %s", err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for the ProfileBundle to become available: %s", err)
	}
	if err := f.AssertMustHaveParsedProfiles(pbName, string(compv1alpha1.ScanTypeNode), "redhat_enterprise_linux_coreos_4"); err != nil {
		t.Fatalf("failed checking profiles in ProfileBundle: %s", err)
	}

	// Check that the rule we removed exists in the original profile
	removedRuleName := prefixName(pbName, removedRule)
	err, found := f.DoesRuleExist(pb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("failed to find rule %s in ProfileBundle %s", removedRuleName, pbName)
	}

	// Check that the rule we unlined in the modified profile is linked in the original
	profilePreUpdate := &compv1alpha1.Profile{}
	profileName := prefixName(pbName, moderateProfileName)
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: pb.Namespace, Name: profileName}, profilePreUpdate); err != nil {
		t.Fatalf("failed to get profile %s", profileName)
	}
	unlinkedRuleName := prefixName(pbName, unlinkedRule)
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePreUpdate)
	if !found {
		t.Fatalf("failed to find rule %s in ProfileBundle %s", unlinkedRuleName, pbName)
	}

	// Update the reference in the image stream
	if err := f.UpdateImageStreamTag(iSName, modifiedImage, f.OperatorNamespace); err != nil {
		t.Fatalf("failed to update image stream %s: %s", iSName, err)
	}

	modifiedImageDigest, err := f.GetImageStreamUpdatedDigest(iSName, f.OperatorNamespace)
	if err != nil {
		t.Fatalf("failed to get digest for image stream %s: %s", iSName, err)
	}

	// Note that when an update happens through an imagestream tag, the operator doesn't get
	// a notification about it... It all happens on the Kube Deployment's side.
	// So we don't need to wait for the profile bundle's statuses
	if err := f.WaitForDeploymentContentUpdate(pbName, modifiedImageDigest); err != nil {
		t.Fatalf("failed waiting for content to update: %s", err)
	}

	if err := f.AssertProfileBundleMustHaveParsedRules(pbName); err != nil {
		t.Fatal(err)
	}

	// We removed this rule in the update, it must no longer exist
	err, found = f.DoesRuleExist(pb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatalf("rule %s unexpectedly found", removedRuleName)
	}

	// This rule was unlinked
	profilePostUpdate := &compv1alpha1.Profile{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: pb.Namespace, Name: profileName}, profilePostUpdate); err != nil {
		t.Fatalf("failed to get profile %s", profileName)
	}
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePostUpdate)
	if found {
		t.Fatalf("rule %s unexpectedly found", unlinkedRuleName)
	}
}

func TestProfileISTagOtherNs(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		removedRule         = "chronyd-no-chronyc-network"
		unlinkedRule        = "chronyd-client-only"
		moderateProfileName = "moderate"
	)
	var (
		baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_baseline")
		modifiedImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "proff_diff_mod")
	)

	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	pbName := framework.GetObjNameFromTest(t)
	iSName := pbName
	otherNs := "openshift"

	stream, err := f.CreateImageStream(iSName, otherNs, baselineImage)
	if err != nil {
		t.Fatalf("failed to create image stream %s\n", iSName)
	}
	defer f.Client.Delete(context.TODO(), stream)

	baselineImage = fmt.Sprintf("%s/%s:%s", otherNs, iSName, "latest")
	pb, err := f.CreateProfileBundle(pbName, baselineImage, framework.RhcosContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle %s: %s", pbName, err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for ProfileBundle to parse: %s", err)
	}
	if err := f.AssertMustHaveParsedProfiles(pbName, string(compv1alpha1.ScanTypeNode), "redhat_enterprise_linux_coreos_4"); err != nil {
		t.Fatalf("failed to assert profiles in ProfileBundle %s: %s", pbName, err)
	}

	// Check that the rule we removed exists in the original profile
	removedRuleName := prefixName(pbName, removedRule)
	err, found := f.DoesRuleExist(pb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("expected rule %s to exist", removedRuleName)
	}

	// Check that the rule we unlined in the modified profile is linked in the original
	profilePreUpdate := &compv1alpha1.Profile{}
	profileName := prefixName(pbName, moderateProfileName)
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: pb.Namespace, Name: profileName}, profilePreUpdate); err != nil {
		t.Fatalf("failed to get profile %s: %s", profileName, err)
	}
	unlinkedRuleName := prefixName(pbName, unlinkedRule)
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePreUpdate)
	if !found {
		t.Fatalf("expected to find rule %s in profile %s", unlinkedRuleName, profileName)
	}

	// Update the reference in the image stream
	if err := f.UpdateImageStreamTag(iSName, modifiedImage, otherNs); err != nil {
		t.Fatalf("failed to update image stream %s: %s", iSName, err)
	}

	modifiedImageDigest, err := f.GetImageStreamUpdatedDigest(iSName, otherNs)
	if err != nil {
		t.Fatalf("failed to get digest for image stream %s: %s", iSName, err)
	}

	// Note that when an update happens through an imagestream tag, the operator doesn't get
	// a notification about it... It all happens on the Kube Deployment's side.
	// So we don't need to wait for the profile bundle's statuses
	if err := f.WaitForDeploymentContentUpdate(pbName, modifiedImageDigest); err != nil {
		t.Fatalf("failed waiting for content to update: %s", err)
	}

	if err := f.AssertProfileBundleMustHaveParsedRules(pbName); err != nil {
		t.Fatal(err)
	}
	// We removed this rule in the update, it must no longer exist
	err, found = f.DoesRuleExist(pb.Namespace, removedRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatalf("rule %s unexpectedly found", removedRuleName)
	}

	// This rule was unlinked
	profilePostUpdate := &compv1alpha1.Profile{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: pb.Namespace, Name: profileName}, profilePostUpdate); err != nil {
		t.Fatalf("failed to get profile %s", profileName)
	}
	found = framework.IsRuleInProfile(unlinkedRuleName, profilePostUpdate)
	if found {
		t.Fatalf("rule %s unexpectedly found", unlinkedRuleName)
	}

}

func TestInvalidBundleWithUnexistentRef(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		unexistentImage = "bad-namespace/bad-image:latest"
	)

	pbName := framework.GetObjNameFromTest(t)
	pb, err := f.CreateProfileBundle(pbName, unexistentImage, framework.RhcosContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle %s: %s", pbName, err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamInvalid); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidBundleWithNoTag(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const (
		noTagImage = "bad-namespace/bad-image"
	)

	pbName := framework.GetObjNameFromTest(t)

	pb, err := f.CreateProfileBundle(pbName, noTagImage, framework.RhcosContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle %s: %s", pbName, err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamInvalid); err != nil {
		t.Fatal(err)
	}
}

func TestServiceMonitoringMetricsTarget(t *testing.T) {
	t.Parallel()
	f := framework.Global

	err := f.SetupRBACForMetricsTest()
	if err != nil {
		t.Fatalf("failed to create service account: %s", err)
	}
	defer f.CleanUpRBACForMetricsTest()

	metricsTargets, err := f.WaitForPrometheusMetricTargets()
	if err != nil {
		t.Fatalf("failed to get prometheus metric targets: %s", err)
	}

	expectedMetricsCount := 2

	err = f.AssertServiceMonitoringMetricsTarget(metricsTargets, expectedMetricsCount)
	if err != nil {
		t.Fatalf("failed to assert metrics target: %s", err)
	}
}

func TestParsingErrorRestartsParserInitContainer(t *testing.T) {
	t.Parallel()
	f := framework.Global
	var (
		badImage  = fmt.Sprintf("%s:%s", brokenContentImagePath, "from")
		goodImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "to")
	)

	pbName := framework.GetObjNameFromTest(t)

	pb, err := f.CreateProfileBundle(pbName, badImage, framework.OcpContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle %s: %s", pbName, err)
	}
	defer f.Client.Delete(context.TODO(), pb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamInvalid); err != nil {
		t.Fatal(err)
	}

	// list the pods with profilebundle=pbName
	var lastErr error
	timeouterr := wait.Poll(framework.RetryInterval, framework.Timeout, func() (bool, error) {
		podList := &corev1.PodList{}
		inNs := client.InNamespace(f.OperatorNamespace)
		withLabel := client.MatchingLabels{"profile-bundle": pbName}
		if lastErr := f.Client.List(context.TODO(), podList, inNs, withLabel); lastErr != nil {
			return false, lastErr
		}

		if len(podList.Items) != 1 {
			return false, fmt.Errorf("expected one parser pod, listed %d", len(podList.Items))
		}
		parserPod := &podList.Items[0]

		// check that pod's initContainerStatuses field with name=profileparser has restartCount > 0 and that
		// lastState.Terminated.ExitCode != 0. This way we'll know we're restarting the init container
		// and retrying the parsing
		for i := range parserPod.Status.InitContainerStatuses {
			ics := parserPod.Status.InitContainerStatuses[i]
			if ics.Name != "profileparser" {
				continue
			}
			if ics.RestartCount < 1 {
				log.Println("The profileparser did not restart (yet?)")
				return false, nil
			}

			// wait until we get the restarted state
			if ics.LastTerminationState.Terminated == nil {
				log.Println("The profileparser does not have terminating state")
				return false, nil
			}
			if ics.LastTerminationState.Terminated.ExitCode == 0 {
				return true, fmt.Errorf("profileparser finished unsuccessfully")
			}
		}

		return true, nil
	})

	if err := framework.ProcessErrorOrTimeout(lastErr, timeouterr, "waiting for ProfileBundle parser to restart"); err != nil {
		t.Fatal(err)
	}

	// Fix the image and wait for the profilebundle to be parsed OK
	getPb := &compv1alpha1.ProfileBundle{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Name: pbName, Namespace: f.OperatorNamespace}, getPb); err != nil {
		t.Fatalf("failed to get ProfileBundle %s: %s", pbName, err)
	}

	updatePb := getPb.DeepCopy()
	updatePb.Spec.ContentImage = goodImage
	if err := f.Client.Update(context.TODO(), updatePb); err != nil {
		t.Fatalf("failed to update ProfileBundle %s: %s", pbName, err)
	}

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatal(err)
	}
	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatal(err)
	}
}

func TestRulesAreClassifiedAppropriately(t *testing.T) {
	t.Parallel()
	f := framework.Global
	for _, expected := range []struct {
		RuleName  string
		CheckType string
	}{
		{
			"ocp4-configure-network-policies-namespaces",
			compv1alpha1.CheckTypePlatform,
		},
		{
			"ocp4-directory-access-var-log-kube-audit",
			compv1alpha1.CheckTypeNode,
		},
		{
			"ocp4-general-apply-scc",
			compv1alpha1.CheckTypeNone,
		},
		{
			"ocp4-kubelet-enable-protect-kernel-sysctl",
			compv1alpha1.CheckTypeNode,
		},
	} {
		targetRule := &compv1alpha1.Rule{}
		key := types.NamespacedName{
			Name:      expected.RuleName,
			Namespace: f.OperatorNamespace,
		}

		if err := f.Client.Get(context.TODO(), key, targetRule); err != nil {
			t.Fatalf("failed to get rule %s: %s", targetRule.Name, err)
		}

		if targetRule.CheckType != expected.CheckType {
			log.Printf("Expected rule '%s' to be of type '%s'. Instead was: '%s'",
				expected.RuleName, expected.CheckType, targetRule.CheckType)
		}
	}
}

func TestSingleScanSucceeds(t *testing.T) {
	t.Parallel()
	f := framework.Global

	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatalf("failed to create scan %s: %s", scanName, err)
	}
	defer f.Client.Delete(context.TODO(), testScan)

	// Verify scanner container security capabilities during running phase
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseRunning)
	if err != nil {
		t.Fatal(err)
	}

	// Assert scanner container has correct capabilities (drops all, only has CAP_SYS_CHROOT)
	pods, err := f.GetPodsForScan(scanName)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) < 1 {
		t.Fatal("No scanner pods found for the scan")
	}

	// Find the scanner container and verify its capabilities
	found := false
	for _, pod := range pods {
		for _, container := range pod.Spec.Containers {
			if container.Name == "scanner" {
				found = true
				if container.SecurityContext == nil {
					t.Fatal("Scanner container has no security context")
				}
				if container.SecurityContext.Capabilities == nil {
					t.Fatal("Scanner container has no capabilities configuration")
				}

				// Verify privileged mode is false
				if container.SecurityContext.Privileged != nil && *container.SecurityContext.Privileged {
					t.Fatal("Expected scanner container to run in non-privileged mode")
				}

				// Verify all capabilities are dropped
				droppedCaps := container.SecurityContext.Capabilities.Drop
				if len(droppedCaps) != 1 || string(droppedCaps[0]) != "ALL" {
					t.Fatalf("Expected scanner container to drop ALL capabilities, got: %v", droppedCaps)
				}

				// Verify CAP_SYS_CHROOT and CAP_SYS_ADMIN are added
				addedCaps := container.SecurityContext.Capabilities.Add
				if len(addedCaps) != 2 {
					t.Fatalf("Expected scanner container to have CAP_SYS_CHROOT and CAP_SYS_ADMIN capabilities, got: %v", addedCaps)
				}
				hasChroot := false
				hasSysAdmin := false
				for _, cap := range addedCaps {
					if string(cap) == "CAP_SYS_CHROOT" {
						hasChroot = true
					}
					if string(cap) == "CAP_SYS_ADMIN" {
						hasSysAdmin = true
					}
				}
				if !hasChroot || !hasSysAdmin {
					t.Fatalf("Expected scanner container to have both CAP_SYS_CHROOT and CAP_SYS_ADMIN capabilities, got: %v", addedCaps)
				}
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		t.Fatal("Scanner container not found in any pod")
	}

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

	aggrString := fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"AGGREGATING\",result=\"NOT-AVAILABLE\"}", scanName)
	metricsSet := map[string]int{
		fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"DONE\",result=\"COMPLIANT\"}", scanName):          1,
		fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"LAUNCHING\",result=\"NOT-AVAILABLE\"}", scanName): 1,
		fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"PENDING\",result=\"\"}", scanName):                1,
		fmt.Sprintf("compliance_operator_compliance_scan_status_total{name=\"%s\",phase=\"RUNNING\",result=\"NOT-AVAILABLE\"}", scanName):   1,
	}

	var metErr error
	// Aggregating may be variable, could be registered 1 to 3 times.
	for i := 1; i < 4; i++ {
		metricsSet[aggrString] = i
		err = framework.AssertEachMetric(f.OperatorNamespace, metricsSet)
		if err == nil {
			metErr = nil
			break
		}
		metErr = err
	}

	if metErr != nil {
		t.Fatalf("failed to assert metrics for scan %s: %s\n", scanName, metErr)
	}

	err = f.AssertScanHasValidPVCReference(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatalf("failed to assert PVC reference for scan %s: %s", scanName, err)
	}
}

func TestSingleScanTimestamps(t *testing.T) {
	t.Parallel()
	f := framework.Global

	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatalf("failed to create scan %s: %s", scanName, err)
	}
	defer f.Client.Delete(context.TODO(), testScan)

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	// assertComplianceCheckResultTimestamps checks that the timestamps are set
	// and that they are set to the same value of startTimestamp of the scan
	err = f.AssertComplianceCheckResultTimestamps(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

	// rerun the scan
	err = f.ReRunScan(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	// assertComplianceCheckResultTimestamps checks that the timestamps are set
	// and that they are set to the same value of startTimestamp of the scan
	err = f.AssertComplianceCheckResultTimestamps(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

}

func TestNonExistentDeprecatedProfile(t *testing.T) {
	t.Parallel()
	f := framework.Global

	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_non_existing_profile",
			Content:      framework.OcpContentFile,
			ContentImage: contentImagePath,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatalf("failed to create scan %s: %s", scanName, err)
	}
	defer f.Client.Delete(context.TODO(), testScan)

	// The profile deprecation warning is sent out during Pending phase
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

	if err = f.Client.Get(context.TODO(), types.NamespacedName{Name: scanName, Namespace: f.OperatorNamespace}, testScan); err != nil {
		t.Fatal(err)
	}
	if testScan.Status.ErrorMessage != "Could not check whether the Profile used by ComplianceScan is deprecated" {
		t.Fatal(errors.New("expected error message to be from failed profile deprecation check"))
	}
}

func TestScanTailoredProfileIsDeprecated(t *testing.T) {
	t.Parallel()
	f := framework.Global

	tpName := "test-tailored-profile-is-deprecated"
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: f.OperatorNamespace,
			Annotations: map[string]string{
				compv1alpha1.ProfileStatusAnnotation: "deprecated",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Extends:     "ocp4-cis",
			Title:       "TestScanTailoredProfileIsDeprecated",
			Description: "TestScanTailoredProfileIsDeprecated",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-cluster-version-operator-exists",
					Rationale: "Test tailored profile extends deprecated",
				},
			},
		},
	}
	err := f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tp)

	suiteName := framework.GetObjNameFromTest(t)
	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     tpName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}
	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// When using SSB with TailoredProfile, the scan has same name as the TP
	scanName := tpName
	if err = f.WaitForProfileDeprecatedWarning(t, scanName, tpName); err != nil {
		t.Fatal(err)
	}

	if err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone); err != nil {
		t.Fatal(err)
	}
}

func TestScanTailoredProfileHasDuplicateVariables(t *testing.T) {
	t.Parallel()
	f := framework.Global
	pbName := framework.GetObjNameFromTest(t)
	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }
	varName := prefixName(pbName, "var-openshift-audit-profile")
	tpName := "test-tailored-profile-has-duplicate-variables"
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Extends:     "ocp4-cis",
			Title:       "TestScanTailoredProfileIsDuplicateVariables",
			Description: "TestScanTailoredProfileIsDuplicateVariables",
			SetValues: []compv1alpha1.VariableValueSpec{
				{
					Name:      varName,
					Rationale: "Value to be set",
					Value:     "WriteRequestBodies",
				},
				{
					Name:      varName,
					Rationale: "Value to be set",
					Value:     "SomethingElse",
				},
			},
		},
	}
	err := f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tp)
	// let's check if the profile is created and if event warning is being generated
	if err = f.WaitForDuplicatedVariableWarning(t, tpName, varName); err != nil {
		t.Fatal(err)
	}

}

func TestScanProducesRemediations(t *testing.T) {
	t.Parallel()
	f := framework.Global
	bindingName := framework.GetObjNameFromTest(t)
	tpName := framework.GetObjNameFromTest(t)

	// When using a profile directly, the profile name gets re-used
	// in the scan. By using a tailored profile we ensure that
	// the scan is unique and we get no clashes.
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "TestScanProducesRemediations",
			Description: "TestScanProducesRemediations",
			Extends:     "ocp4-moderate",
		},
	}

	createTPErr := f.Client.Create(context.TODO(), tp, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tp)
	scanSettingBinding := compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				Name:     tpName,
				Kind:     "TailoredProfile",
				APIGroup: "compliance.openshift.io/v1alpha1",
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			Name:     "default",
			Kind:     "ScanSetting",
			APIGroup: "compliance.openshift.io/v1alpha1",
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), &scanSettingBinding, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingBinding)
	if err := f.WaitForSuiteScansStatus(f.OperatorNamespace, bindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant); err != nil {
		t.Fatal(err)
	}

	// Since the scan was not compliant, there should be some remediations and none
	// of them should be an error
	inNs := client.InNamespace(f.OperatorNamespace)
	withLabel := client.MatchingLabels{compv1alpha1.SuiteLabel: bindingName}
	fmt.Println(inNs, withLabel)
	remList := &compv1alpha1.ComplianceRemediationList{}
	err = f.Client.List(context.TODO(), remList, inNs, withLabel)
	if err != nil {
		t.Fatal(err)
	}

	if len(remList.Items) == 0 {
		t.Fatal("expected at least one remediation")
	}
	for _, rem := range remList.Items {
		if rem.Status.ApplicationState != compv1alpha1.RemediationNotApplied {
			t.Fatal("expected all remediations are unapplied when scan finishes")
		}
	}
}

func TestSingleScanWithStorageSucceeds(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				RawResultStorage: compv1alpha1.RawResultStorageSettings{
					Size: "2Gi",
				},
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanHasValidPVCReferenceWithSize(scanName, "2Gi", f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertARFReportExistsInPVC(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithUnexistentResourceFails(t *testing.T) {
	// This tests scan behavior when Kubernetes resource doesn't exist
	// The data stream, content image and profile all exist
	t.Parallel()
	f := framework.Global
	pbName := framework.GetObjNameFromTest(t)
	var unexistentImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "unexistent_resource")
	origPb, err := f.CreateProfileBundle(pbName, unexistentImage, framework.UnexistentResourceContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle: %s", err)
	}
	// This should get cleaned up at the end of the test
	defer f.Client.Delete(context.TODO(), origPb)
	if err = f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for the ProfileBundle to become available: %s", err)
	}

	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_test",
			Content:      framework.UnexistentResourceContentFile,
			ContentImage: unexistentImage,
			Rule:         "xccdf_org.ssgproject.content_rule_api_server_unexistent_resource",
			ScanType:     compv1alpha1.ScanTypePlatform,
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsNonCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

	if err = f.ScanHasWarnings(scanName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}
}

func TestScanStorageOutOfLimitRangeFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	// Create LimitRange
	lr := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pvc-limitrange",
			Namespace: f.OperatorNamespace,
		},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{
				{
					Type: corev1.LimitTypePersistentVolumeClaim,
					Max: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
		},
	}
	if err := f.Client.Create(context.TODO(), lr, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), lr)

	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				RawResultStorage: compv1alpha1.RawResultStorageSettings{
					Size: "6Gi",
				},
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testScan)
	f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

}

func TestSingleTailoredScanSucceeds(t *testing.T) {
	t.Parallel()
	f := framework.Global

	tpName := "test-tailoredprofile"
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: f.OperatorNamespace,
			Annotations: map[string]string{
				compv1alpha1.ProductTypeAnnotation: "Node",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "TestSingleTailoredScanSucceeds",
			Description: "TestSingleTailoredScanSucceeds",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "rhcos4-no-netrc-files",
					Rationale: "Test for platform profile tailoring",
				},
			},
			DisableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "rhcos4-audit-rules-dac-modification-chmod",
					Rationale: "Disable rule for testing",
				},
			},
			SetValues: []compv1alpha1.VariableValueSpec{
				{
					Name:      "rhcos4-var-selinux-state",
					Rationale: "Set variable value for testing",
					Value:     "permissive",
				},
			},
		},
	}
	err := f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tp)

	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the tailored profile details through ConfigMap
	tpConfigMapName := fmt.Sprintf("%s-tp", tpName)
	tpConfigMap := &corev1.ConfigMap{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{
		Name:      tpConfigMapName,
		Namespace: f.OperatorNamespace,
	}, tpConfigMap)
	if err != nil {
		t.Fatal(err)
	}

	tailoringData, ok := tpConfigMap.Data["tailoring.xml"]
	if !ok {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"\"xccdf_org.ssgproject.content_rule_no_netrc_files\" selected=\"true\"",
		"\"xccdf_org.ssgproject.content_rule_audit_rules_dac_modification_chmod\" selected=\"false\"",
		"\"xccdf_org.ssgproject.content_value_var_selinux_state\">permissive",
	} {
		if !strings.Contains(tailoringData, expected) {
			t.Fatal(err)
		}
	}

	suiteName := framework.GetObjNameFromTest(t)
	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     tpName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}
	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// When using SSB with TailoredProfile, the scan has same name as the TP
	scanNameMaster := fmt.Sprintf("%s-master", tpName)
	scanNameWorker := fmt.Sprintf("%s-worker", tpName)
	if err = f.WaitForScanStatus(f.OperatorNamespace, scanNameMaster, compv1alpha1.PhaseDone); err != nil {
		t.Fatal(err)
	}
	if err = f.AssertScanIsCompliant(scanNameMaster, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}
	if err = f.WaitForScanStatus(f.OperatorNamespace, scanNameWorker, compv1alpha1.PhaseDone); err != nil {
		t.Fatal(err)
	}
	if err = f.AssertScanIsCompliant(scanNameWorker, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}
}

// TestSingleTailoredPlatformScanSucceedsOptionalProxy tests TailoredProfiles created from scratch
// without extending an existing Profile.
func TestSingleTailoredPlatformScanSucceedsOptionalProxy(t *testing.T) {
	t.Parallel()
	f := framework.Global

	// Check if cluster is proxy and verify deployment env vars if so
	var httpsProxy string
	proxy := &configv1.Proxy{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, proxy); err == nil {
		httpsProxy = proxy.Spec.HTTPSProxy
		if httpsProxy != "" {
			deployment, err := f.KubeClient.AppsV1().Deployments(f.OperatorNamespace).Get(context.TODO(), "compliance-operator", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get compliance-operator deployment: %s", err)
			}
			if len(deployment.Spec.Template.Spec.Containers) == 0 {
				t.Fatal("compliance-operator deployment has no containers")
			}

			envMap := make(map[string]string)
			for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "HTTPS_PROXY" {
					envMap[env.Name] = env.Value
				}
			}
			if httpsProxy != "" && envMap["HTTPS_PROXY"] != httpsProxy {
				t.Fatalf("HTTPS_PROXY mismatch. Expected: %s, Got: %s", httpsProxy, envMap["HTTPS_PROXY"])
			}
		}
	}

	// Test 1: Platform-only TailoredProfile from scratch
	tpPlatformName := "test-tailoredplatformprofile"
	tpPlatform := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpPlatformName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Platform profile from scratch",
			Description: "Test platform profile created without extending",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-cluster-version-operator-exists",
					Rationale: "Test for platform profile tailoring",
				},
				{
					Name:      "ocp4-api-server-admission-control-plugin-alwaysadmit",
					Rationale: "Platform rule",
				},
			},
			DisableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-api-server-admission-control-plugin-alwayspullimages",
					Rationale: "Platform disable rule",
				},
			},
		},
	}
	err := f.Client.Create(context.TODO(), tpPlatform, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tpPlatform)

	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpPlatformName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	// Test 2: Node-only TailoredProfile from scratch
	tpNodeName := "test-tailorednodeprofile"
	tpNode := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpNodeName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Node profile from scratch",
			Description: "Test node profile created without extending",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-file-groupowner-cni-conf",
					Rationale: "Node rule",
				},
				{
					Name:      "ocp4-accounts-restrict-service-account-tokens",
					Rationale: "Node rule",
				},
			},
			DisableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-file-groupowner-etcd-data-dir",
					Rationale: "Node disable rule",
				},
			},
		},
	}
	err = f.Client.Create(context.TODO(), tpNode, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tpNode)

	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpNodeName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	// Test 3: TailoredProfile with both node and platform rules should error
	tpMixedName := "test-tailoredmixedprofile"
	tpMixed := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpMixedName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Mixed profile from scratch",
			Description: "Test profile with both node and platform rules",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-file-groupowner-cni-conf",
					Rationale: "Node rule",
				},
				{
					Name:      "ocp4-api-server-admission-control-plugin-alwayspullimages",
					Rationale: "Platform rule",
				},
			},
			DisableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-file-groupowner-etcd-data-dir",
					Rationale: "Node disable rule",
				},
				{
					Name:      "ocp4-api-server-admission-control-plugin-alwaysadmit",
					Rationale: "Platform disable rule",
				},
			},
		},
	}
	err = f.Client.Create(context.TODO(), tpMixed, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tpMixed)

	// Verify it goes to ERROR state
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpMixedName, compv1alpha1.TailoredProfileStateError)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the error message contains expected text
	tpMixedObj := &compv1alpha1.TailoredProfile{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: tpMixedName, Namespace: f.OperatorNamespace}, tpMixedObj)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tpMixedObj.Status.ErrorMessage, "didn't match expected type") {
		t.Fatalf("Expected error message to contain 'didn't match expected type', got: %s", tpMixedObj.Status.ErrorMessage)
	}

	// Test 4: Patch the mixed TailoredProfile to fix it (remove conflicting rules)
	// Remove disableRules first
	patchRemoveDisable := []byte(`[{"op": "remove", "path": "/spec/disableRules"}]`)
	err = f.Client.Patch(context.TODO(), tpMixedObj, client.RawPatch(types.JSONPatchType, patchRemoveDisable))
	if err != nil {
		t.Fatalf("Failed to patch TailoredProfile to remove disableRules: %s", err)
	}

	// Remove the first enableRule (node rule) to make it platform-only
	patchRemoveFirstEnable := []byte(`[{"op": "remove", "path": "/spec/enableRules/0"}]`)
	err = f.Client.Patch(context.TODO(), tpMixedObj, client.RawPatch(types.JSONPatchType, patchRemoveFirstEnable))
	if err != nil {
		t.Fatalf("Failed to patch TailoredProfile to remove enableRules[0]: %s", err)
	}

	// Verify it becomes READY after patching
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpMixedName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	// Test 5: Validate that title field is required
	tpNoTitle := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-notitle",
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Description: "Profile without title",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-file-groupowner-cni-conf",
					Rationale: "Node rule",
				},
			},
		},
	}
	err = f.Client.Create(context.TODO(), tpNoTitle, nil)
	if err == nil {
		f.Client.Delete(context.TODO(), tpNoTitle)
		t.Fatal("Expected error when creating TailoredProfile without title, but got none")
	}
	if !strings.Contains(err.Error(), "spec.title") && !strings.Contains(err.Error(), "Required value") {
		t.Fatalf("Expected error about missing title, got: %s", err)
	}

	// Test 6: Validate that description field is required
	tpNoDesc := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nodesc",
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title: "Profile without description",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-file-groupowner-cni-conf",
					Rationale: "Node rule",
				},
			},
		},
	}
	err = f.Client.Create(context.TODO(), tpNoDesc, nil)
	if err == nil {
		f.Client.Delete(context.TODO(), tpNoDesc)
		t.Fatal("Expected error when creating TailoredProfile without description, but got none")
	}
	if !strings.Contains(err.Error(), "spec.description") && !strings.Contains(err.Error(), "Required value") {
		t.Fatalf("Expected error about missing description, got: %s", err)
	}

	// Test 7: Run scan with platform TailoredProfile
	suiteName := framework.GetObjNameFromTest(t)
	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     tpPlatformName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}
	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// When using SSB with TailoredProfile, the scan has same name as the TP
	scanName := tpPlatformName
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}

	// Test 8: Run scan with node TailoredProfile
	ssbNodeName := suiteName + "-node"
	ssbNode := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ssbNodeName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     tpNodeName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}
	err = f.Client.Create(context.TODO(), ssbNode, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ssbNode)

	// Wait for node scan to complete
	scanNodeName := tpNodeName
	err = f.WaitForScanStatus(f.OperatorNamespace, scanNodeName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	// If proxy cluster, verify httpsProxy in configmap
	// CO only propagates and uses httpsProxy
	if httpsProxy != "" {
		cm := &corev1.ConfigMap{}
		cmName := scanName + "-openscap-env-map"
		if err := f.Client.Get(context.TODO(), types.NamespacedName{Name: cmName, Namespace: f.OperatorNamespace}, cm); err != nil && apierrors.IsNotFound(err) {
			cmList := &corev1.ConfigMapList{}
			if err := f.Client.List(context.TODO(), cmList, client.InNamespace(f.OperatorNamespace), client.MatchingLabels{
				compv1alpha1.ComplianceScanLabel: scanName,
				compv1alpha1.ScriptLabel:         "",
			}); err != nil {
				t.Fatalf("failed to list ConfigMaps: %s", err)
			}
			for i := range cmList.Items {
				if strings.Contains(cmList.Items[i].Name, "openscap-env-map") && cmList.Items[i].Data["HTTPS_PROXY"] != "" {
					cm = &cmList.Items[i]
					break
				}
			}
		} else if err != nil {
			t.Fatalf("failed to get ConfigMap: %s", err)
		}

		if cm.Data["HTTPS_PROXY"] != httpsProxy {
			t.Fatalf("HTTPS_PROXY mismatch in configmap. Expected: %s, Got: %s", httpsProxy, cm.Data["HTTPS_PROXY"])
		}
	}
}

func TestScanWithNodeSelectorFiltersCorrectly(t *testing.T) {
	t.Parallel()
	f := framework.Global
	selectWorkers := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}
	testComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-filtered-scan",
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			NodeSelector: selectWorkers,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, "test-filtered-scan", compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := f.GetNodesWithSelector(selectWorkers)
	if err != nil {
		t.Fatal(err)
	}
	configmaps, err := f.GetConfigMapsFromScan(testComplianceScan)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != len(configmaps) {
		t.Fatalf("The number of reports doesn't match the number of selected nodes: %d reports / %d nodes", len(configmaps), len(nodes))
	}
	err = f.AssertScanIsCompliant("test-filtered-scan", f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithNodeSelectorNoMatches(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := framework.GetObjNameFromTest(t)
	selectNone := map[string]string{
		"node-role.kubernetes.io/no-matches": "",
	}
	testComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			NodeSelector: selectNone,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug:             true,
				ShowNotApplicable: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsNotApplicable(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithInvalidScanTypeFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      "ssg-ocp4-non-existent.xml",
			ContentImage: contentImagePath,
			ScanType:     "BadScanType",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithInvalidContentFails(t *testing.T) {
	// This test logs a "Could not get Profile" error, but that is expected
	t.Parallel()
	f := framework.Global
	scanName := "test-scan-w-invalid-content"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      "ssg-ocp4-non-existent.xml",
			ContentImage: contentImagePath,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithInvalidProfileFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-scan-w-invalid-profile"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_coreos-unexistent",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMalformedTailoredScanFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	cmName := "test-malformed-tailored-scan-fails-cm"
	tailoringCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: f.OperatorNamespace,
		},
		// The tailored profile's namespace is wrong. It should be xccdf-1.2, but it was
		// declared as xccdf. So it should report an error
		Data: map[string]string{
			"tailoring.xml": `<?xml version="1.0" encoding="UTF-8"?>
<xccdf-1.2:Tailoring xmlns:xccdf="http://checklists.nist.gov/xccdf/1.2" id="xccdf_compliance.openshift.io_tailoring_test-tailoredprofile">
<xccdf-1.2:benchmark href="/content/ssg-rhcos4-ds.xml"></xccdf-1.2:benchmark>
<xccdf-1.2:version time="2020-04-28T07:04:13Z">1</xccdf-1.2:version>
<xccdf-1.2:Profile id="xccdf_compliance.openshift.io_profile_test-tailoredprofile">
<xccdf-1.2:title>Test Tailored Profile</xccdf-1.2:title>
<xccdf-1.2:description>Test Tailored Profile</xccdf-1.2:description>
<xccdf-1.2:select idref="xccdf_org.ssgproject.content_rule_no_netrc_files" selected="true"></xccdf-1.2:select>
</xccdf-1.2:Profile>
</xccdf-1.2:Tailoring>`,
		},
	}

	err := f.Client.Create(context.TODO(), tailoringCM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tailoringCM)

	scanName := "test-malformed-tailored-scan-fails"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_compliance.openshift.io_profile_test-tailoredprofile",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
			TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
				Name: tailoringCM.Name,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithEmptyTailoringCMNameFails(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-scan-w-empty-tailoring-cm"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
				Name: "",
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsInError(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanWithMissingTailoringCMFailsAndRecovers(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-scan-w-missing-tailoring-cm"

	tpName := "test-tailoredprofile-missing-cm"
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "TestScanWithMissingTailoringCMFailsAndRecovers",
			Description: "TestScanWithMissingTailoringCMFailsAndRecovers",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "rhcos4-no-netrc-files",
					Rationale: "Test for platform profile tailoring missing CM fails and recovers",
				},
			},
		},
	}
	createTPErr := f.Client.Create(context.TODO(), tp, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tp)

	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_compliance.openshift.io_profile_test-tailoredprofile-missing-cm",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
			TailoringConfigMap: &compv1alpha1.TailoringConfigMapRef{
				Name: "missing-tailoring-file",
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseLaunching)
	if err != nil {
		t.Fatal(err)
	}

	var resultErr error
	// The status might still be NOT-AVAILABLE... we can wait a bit
	// for the reconciliation to update it.
	_ = wait.PollImmediate(framework.RetryInterval, framework.Timeout, func() (bool, error) {
		if resultErr = f.AssertScanIsInError(scanName, f.OperatorNamespace); resultErr != nil {
			return false, nil
		}
		return true, nil
	})
	if resultErr != nil {
		t.Fatalf("failed waiting for the config map: %s", resultErr)
	}

	tailoringCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "missing-tailoring-file",
			Namespace: f.OperatorNamespace,
		},
		Data: map[string]string{
			"tailoring.xml": `<?xml version="1.0" encoding="UTF-8"?>
<xccdf-1.2:Tailoring xmlns:xccdf-1.2="http://checklists.nist.gov/xccdf/1.2" id="xccdf_compliance.openshift.io_tailoring_test-tailoredprofile-missing-cm">
<xccdf-1.2:benchmark href="/content/ssg-rhcos4-ds.xml"></xccdf-1.2:benchmark>
<xccdf-1.2:version time="2020-04-28T07:04:13Z">1</xccdf-1.2:version>
<xccdf-1.2:Profile id="xccdf_compliance.openshift.io_profile_test-tailoredprofile-missing-cm">
<xccdf-1.2:title>Test Tailored Profile</xccdf-1.2:title>
<xccdf-1.2:description>Test Tailored Profile</xccdf-1.2:description>
<xccdf-1.2:select idref="xccdf_org.ssgproject.content_rule_no_netrc_files" selected="true"></xccdf-1.2:select>
</xccdf-1.2:Profile>
</xccdf-1.2:Tailoring>`,
		},
	}
	err = f.Client.Create(context.TODO(), tailoringCM, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tailoringCM)

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}
	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMissingPodInRunningState(t *testing.T) {
	t.Parallel()
	f := framework.Global
	scanName := "test-missing-pod-scan"
	exampleComplianceScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile:      "xccdf_org.ssgproject.content_profile_moderate",
			Content:      framework.RhcosContentFile,
			ContentImage: contentImagePath,
			Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), exampleComplianceScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), exampleComplianceScan)

	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseRunning)
	if err != nil {
		t.Fatal(err)
	}
	pods, err := f.GetPodsForScan(scanName)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) < 1 {
		t.Fatal("No pods gotten from query for the scan")
	}
	podToDelete := pods[rand.Intn(len(pods))]
	// Delete pod ASAP
	zeroSeconds := int64(0)
	do := client.DeleteOptions{GracePeriodSeconds: &zeroSeconds}
	err = f.Client.Delete(context.TODO(), &podToDelete, &do)
	if err != nil {
		t.Fatal(err)
	}
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	err = f.AssertScanIsCompliant(scanName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyGenericRemediation(t *testing.T) {
	t.Parallel()
	f := framework.Global
	remName := "test-apply-generic-remediation"
	unstruct := &unstructured.Unstructured{}
	unstruct.SetUnstructuredContent(map[string]interface{}{
		"kind":       "ConfigMap",
		"apiVersion": "v1",
		"metadata": map[string]interface{}{
			"name":      "generic-rem-cm",
			"namespace": f.OperatorNamespace,
		},
		"data": map[string]interface{}{
			"key": "value",
		},
	})

	genericRem := &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: true,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: unstruct,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), genericRem, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), genericRem)
	err = f.WaitForRemediationState(remName, f.OperatorNamespace, compv1alpha1.RemediationApplied)
	if err != nil {
		t.Fatal(err)
	}

	cm := &corev1.ConfigMap{}
	cmName := "generic-rem-cm"
	err = f.WaitForObjectToExist(cmName, f.OperatorNamespace, cm)
	if err != nil {
		t.Fatal(err)
	}
	val, ok := cm.Data["key"]
	if !ok || val != "value" {
		t.Fatalf("ComplianceRemediation '%s' generated a malformed ConfigMap", remName)
	}

	// verify object is marked as created by the operator
	if !compv1alpha1.RemediationWasCreatedByOperator(cm) {
		t.Fatalf("ComplianceRemediation '%s' is missing controller annotation '%s'",
			remName, compv1alpha1.RemediationCreatedByOperatorAnnotation)
	}
}

func TestPatchGenericRemediation(t *testing.T) {
	t.Parallel()
	f := framework.Global
	remName := framework.GetObjNameFromTest(t)
	cmName := remName
	cmKey := types.NamespacedName{
		Name:      cmName,
		Namespace: f.OperatorNamespace,
	}
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmKey.Name,
			Namespace: cmKey.Namespace,
		},
		Data: map[string]string{
			"existingKey": "existingData",
		},
	}

	if err := f.Client.Create(context.TODO(), existingCM, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), existingCM)

	cm := &corev1.ConfigMap{}
	err := f.WaitForObjectToExist(cmKey.Name, f.OperatorNamespace, cm)
	if err != nil {
		t.Fatal(err)
	}

	unstruct := &unstructured.Unstructured{}
	unstruct.SetUnstructuredContent(map[string]interface{}{
		"kind":       "ConfigMap",
		"apiVersion": "v1",
		"metadata": map[string]interface{}{
			"name":      cmKey.Name,
			"namespace": cmKey.Namespace,
		},
		"data": map[string]interface{}{
			"newKey": "newData",
		},
	})

	genericRem := &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: true,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: unstruct,
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(context.TODO(), genericRem, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), genericRem)

	err = f.WaitForRemediationState(remName, f.OperatorNamespace, compv1alpha1.RemediationApplied)
	if err != nil {
		t.Fatal(err)
	}

	err = f.WaitForObjectToUpdate(cmKey.Name, f.OperatorNamespace, cm)
	if err != nil {
		t.Fatal(err)
	}

	// Old data should still be there
	val, ok := cm.Data["existingKey"]
	if !ok || val != "existingData" {
		t.Fatalf("ComplianceRemediation '%s' generated a malformed ConfigMap", remName)
	}

	// new data should be there too
	val, ok = cm.Data["newKey"]
	if !ok || val != "newData" {
		t.Fatalf("ComplianceRemediation '%s' generated a malformed ConfigMap", remName)
	}
}

func TestGenericRemediationFailsWithUnknownType(t *testing.T) {
	t.Parallel()
	f := framework.Global
	remName := "test-generic-remediation-fails-unknown"
	genericRem := &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: true,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"kind":       "OopsyDoodle",
						"apiVersion": "foo.bar/v1",
						"metadata": map[string]interface{}{
							"name":      "unknown-remediation",
							"namespace": f.OperatorNamespace,
						},
						"data": map[string]interface{}{
							"key": "value",
						},
					},
				},
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), genericRem, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), genericRem)
	err = f.WaitForRemediationState(remName, f.OperatorNamespace, compv1alpha1.RemediationError)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSuiteWithInvalidScheduleShowsError(t *testing.T) {
	t.Parallel()
	f := framework.Global
	suiteName := "test-suite-with-invalid-schedule"
	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
				Schedule:              "This is WRONG",
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: fmt.Sprintf("%s-workers-scan", suiteName),
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: contentImagePath,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      framework.RhcosContentFile,
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							Debug: true,
						},
						NodeSelector: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultError)
	if err != nil {
		t.Fatal(err)
	}
	err = f.SuiteErrorMessageMatchesRegex(f.OperatorNamespace, suiteName, "Suite was invalid: .*")
	if err != nil {
		t.Fatal(err)
	}
}

func TestScheduledSuite(t *testing.T) {
	t.Parallel()
	f := framework.Global
	suiteName := "test-scheduled-suite"

	workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)
	selectWorkers := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}

	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
				Schedule:              "*/2 * * * *",
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: workerScanName,
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: contentImagePath,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      framework.RhcosContentFile,
						Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
						NodeSelector: selectWorkers,
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							RawResultStorage: compv1alpha1.RawResultStorageSettings{
								Rotation: 1,
							},
							Debug: true,
						},
					},
				},
			},
		},
	}

	err := f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	// Ensure that all the scans in the suite have finished and are marked as Done
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for one re-scan
	err = f.WaitForReScanStatus(f.OperatorNamespace, workerScanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for a second one to assert this is running scheduled as expected
	err = f.WaitForReScanStatus(f.OperatorNamespace, workerScanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	// Remove cronjob so it doesn't keep running while other tests are
	// running. Use Patch instead of Update to avoid resource version
	// conflicts with the controller.
	patch := []byte(`{"spec":{"schedule":""}}`)
	if err = f.Client.Patch(context.TODO(), testSuite, client.RawPatch(types.MergePatchType, patch)); err != nil {
		t.Fatal(err)
	}

	rawResultClaimName, err := f.GetRawResultClaimNameFromScan(f.OperatorNamespace, workerScanName)
	if err != nil {
		t.Fatal(err)
	}

	rotationCheckerPod := framework.GetRotationCheckerWorkload(f.OperatorNamespace, rawResultClaimName)
	if err = f.Client.Create(context.TODO(), rotationCheckerPod, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), rotationCheckerPod)

	err = f.AssertResultStorageHasExpectedItemsAfterRotation(1, f.OperatorNamespace, rotationCheckerPod.Name)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScheduledSuitePriorityClass(t *testing.T) {
	t.Parallel()
	f := framework.Global
	suiteName := "test-scheduled-suite-priority-class"
	workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)
	selectWorkers := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}

	priorityClass := &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "e2e-compliance-suite-high-priority",
		},
		Value: 100,
	}

	// Ensure that the priority class is created
	err := f.Client.Create(context.TODO(), priorityClass, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), priorityClass)

	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: workerScanName,
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: contentImagePath,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      framework.RhcosContentFile,
						Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
						NodeSelector: selectWorkers,
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							PriorityClass: "e2e-compliance-suite-high-priority",
							RawResultStorage: compv1alpha1.RawResultStorageSettings{
								Rotation: 1,
							},
							Debug: true,
						},
					},
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	podList := &corev1.PodList{}
	err = f.Client.List(context.TODO(), podList, client.InNamespace(f.OperatorNamespace), client.MatchingLabels(map[string]string{
		"workload": "scanner",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// check if the scanning pod has properly been created and has priority class set
	for _, pod := range podList.Items {
		if strings.Contains(pod.Name, workerScanName) {
			if err := framework.WaitForPod(framework.CheckPodPriorityClass(f.KubeClient, pod.Name, f.OperatorNamespace, "e2e-compliance-suite-high-priority")); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Ensure that all the scans in the suite have finished and are marked as Done
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScheduledSuiteNoStorage(t *testing.T) {
	t.Parallel()
	f := framework.Global
	suiteName := "test-scheduled-suite-no-storage"
	workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)
	selectWorkers := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}

	falseValue := false
	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: workerScanName,
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: contentImagePath,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      framework.RhcosContentFile,
						Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
						NodeSelector: selectWorkers,
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							RawResultStorage: compv1alpha1.RawResultStorageSettings{
								Enabled: &falseValue,
							},
							Debug: true,
						},
					},
				},
			},
		},
	}

	err := f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	pvcList := &corev1.PersistentVolumeClaimList{}
	err = f.Client.List(context.TODO(), pvcList, client.InNamespace(f.OperatorNamespace), client.MatchingLabels(map[string]string{
		compv1alpha1.ComplianceScanLabel: workerScanName,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pvcList.Items) > 0 {
		for _, pvc := range pvcList.Items {
			t.Fatalf("Found unexpected PVC %s", pvc.Name)
		}
		t.Fatal("Expected not to find PVC associated with the scan.")
	}

	// Ensure that all the scans in the suite have finished and are marked as Done
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScheduledSuiteInvalidPriorityClass(t *testing.T) {
	t.Parallel()
	f := framework.Global
	suiteName := "test-scheduled-suite-invalid-priority-class"

	workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)
	selectWorkers := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}

	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: workerScanName,
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: contentImagePath,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      framework.RhcosContentFile,
						Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
						NodeSelector: selectWorkers,
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							PriorityClass: "priority-invalid",
							RawResultStorage: compv1alpha1.RawResultStorageSettings{
								Rotation: 1,
							},
							Debug: true,
						},
					},
				},
			},
		},
	}

	err := f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	podList := &corev1.PodList{}
	err = f.Client.List(context.TODO(), podList, client.InNamespace(f.OperatorNamespace), client.MatchingLabels(map[string]string{
		"workload": "scanner",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// check if the scanning pod has properly been created and has priority class set
	for _, pod := range podList.Items {
		if strings.Contains(pod.Name, workerScanName) {
			if err := framework.WaitForPod(framework.CheckPodPriorityClass(f.KubeClient, pod.Name, f.OperatorNamespace, "")); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Ensure that all the scans in the suite have finished and are marked as Done
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
	if err != nil {
		t.Fatal(err)
	}
}

func TestScheduledSuiteUpdate(t *testing.T) {
	t.Parallel()
	f := framework.Global
	suiteName := framework.GetObjNameFromTest(t)
	workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)
	selectWorkers := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}

	initialSchedule := "0 * * * *"
	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
				Schedule:              initialSchedule,
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: workerScanName,
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: contentImagePath,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      framework.RhcosContentFile,
						Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
						NodeSelector: selectWorkers,
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							Debug: true,
						},
					},
				},
			},
		},
	}

	err := f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	// Ensure that all the scans in the suite have finished and are marked as Done
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
	if err != nil {
		t.Fatal(err)
	}

	err = f.WaitForCronJobWithSchedule(f.OperatorNamespace, suiteName, initialSchedule)
	if err != nil {
		t.Fatal(err)
	}

	// Get new reference of suite
	foundSuite := &compv1alpha1.ComplianceSuite{}
	key := types.NamespacedName{Name: testSuite.Name, Namespace: testSuite.Namespace}
	if err = f.Client.Get(context.TODO(), key, foundSuite); err != nil {
		t.Fatal(err)
	}

	// Update schedule
	testSuiteCopy := foundSuite.DeepCopy()
	updatedSchedule := "*/2 * * * *"
	testSuiteCopy.Spec.Schedule = updatedSchedule
	if err = f.Client.Update(context.TODO(), testSuiteCopy); err != nil {
		t.Fatal(err)
	}

	if err = f.WaitForCronJobWithSchedule(f.OperatorNamespace, suiteName, updatedSchedule); err != nil {
		t.Fatal(err)
	}

	// Clean up
	// Get new reference of suite
	foundSuite = &compv1alpha1.ComplianceSuite{}
	if err = f.Client.Get(context.TODO(), key, foundSuite); err != nil {
		t.Fatal(err)
	}

	// Remove cronjob so it doesn't keep running while other tests are running
	testSuiteCopy = foundSuite.DeepCopy()
	updatedSchedule = ""
	testSuiteCopy.Spec.Schedule = updatedSchedule
	if err = f.Client.Update(context.TODO(), testSuiteCopy); err != nil {
		t.Fatal(err)
	}
}

// TestCustomRuleTailoredProfile tests CustomRule functionality with TailoredProfiles
func TestCustomRuleTailoredProfile(t *testing.T) {
	t.Parallel()
	f := framework.Global

	testName := framework.GetObjNameFromTest(t)
	customRuleName := fmt.Sprintf("%s-security-context", testName)
	tpName := fmt.Sprintf("%s-tp", testName)
	ssbName := fmt.Sprintf("%s-ssb", testName)
	testNamespace := f.OperatorNamespace

	// Create a unique label for our test pods to ensure isolation
	// Only pods with this label will be checked by the CustomRule
	testLabel := fmt.Sprintf("test-customrule-%s", testName)
	// Create a pod without our test label to verify it's NOT checked by the rule
	ignoredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-ignored-pod", testName),
			Namespace: testNamespace,
			// NO label - this pod should be ignored by our CustomRule
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "ignored-container",
					Image:   "busybox:latest",
					Command: []string{"sh", "-c", "sleep 3600"},
				},
			},
			// No security context, but should be ignored
		},
	}

	err := f.Client.Create(context.TODO(), ignoredPod, nil)
	if err != nil {
		t.Fatalf("Failed to create ignored pod: %v", err)
	}
	defer f.Client.Delete(context.TODO(), ignoredPod)

	// Create CustomRule that only checks our test pods
	customRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      customRuleName,
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          customRuleName,
				Title:       "Test Pods Must Have Security Context",
				Description: fmt.Sprintf("Ensures test pods with label customrule-test=%s have proper security context", testLabel),
				Severity:    "high",
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL,
				Expression: fmt.Sprintf(`
					pods.items.filter(pod,
						has(pod.metadata.labels) &&
						"customrule-test" in pod.metadata.labels &&
						pod.metadata.labels["customrule-test"] == "%s"
					).all(pod,
						has(pod.spec.securityContext) &&
						pod.spec.securityContext.runAsNonRoot == true
					)
				`, testLabel),
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion:        "v1",
							Resource:          "pods",
							ResourceNamespace: testNamespace,
						},
					},
				},
				FailureReason: fmt.Sprintf("Test pod(s) with label customrule-test=%s found without proper security context (runAsNonRoot must be true)", testLabel),
			},
		},
	}

	err = f.Client.Create(context.TODO(), customRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), customRule)

	// Wait for CustomRule to be validated and ready
	err = f.WaitForCustomRuleStatus(testNamespace, customRuleName, "Ready")
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}

	// Create TailoredProfile with CustomRule
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: testNamespace,
			Annotations: map[string]string{
				compv1alpha1.DisableOutdatedReferenceValidation: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Custom Security Checks",
			Description: "Test profile using CEL-based CustomRules",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      customRuleName,
					Kind:      "CustomRule",
					Rationale: "Security best practice requires pods to run as non-root",
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatalf("Failed to create TailoredProfile: %v", err)
	}
	defer f.Client.Delete(context.TODO(), tp)

	// Create ScanSettingBinding
	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ssbName,
			Namespace: testNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     tpName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}

	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatalf("Failed to create ScanSettingBinding: %v", err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// Wait for suite to be created and for scans to complete
	suiteName := ssbName

	// Wait for scans to complete
	// The scan should be NON-COMPLIANT because our test pod doesn't have the required security context
	err = f.WaitForSuiteScansStatus(testNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
	if err != nil {
		t.Fatalf("Scan did not complete as expected: %v", err)
	}
	// Create a test pod without security context (should fail the check)
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-test-pod", testName),
			Namespace: testNamespace,
			Labels: map[string]string{
				"customrule-test": testLabel,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "test-container",
					Image:   "busybox:latest",
					Command: []string{"sh", "-c", "sleep 3600"},
				},
			},
			// Deliberately not setting securityContext to test the CustomRule
		},
	}

	// Create test pod
	err = f.Client.Create(context.TODO(), testPod, nil)
	if err != nil {
		t.Fatalf("Failed to create test pod: %v", err)
	}
	defer f.Client.Delete(context.TODO(), testPod)

	suite := &compv1alpha1.ComplianceSuite{}
	key := types.NamespacedName{Name: suiteName, Namespace: testNamespace}
	if err := f.Client.Get(context.TODO(), key, suite); err != nil {
		t.Fatal(err)
	}
	// Annotate all ComplianceScans in the suite to generate fresh results
	err = f.RescanSuite(suiteName, testNamespace)
	if err != nil {
		t.Fatalf("Failed to rescan suite: %s", err)
	}
	err = f.WaitForSuiteScansStatus(testNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatalf("Scan did not complete as expected: %v", err)
	}

	// Validate that the CustomRule result is FAIL
	// For TailoredProfiles, the scan name is the TailoredProfile name
	scanName := tpName
	expectedCheck := compv1alpha1.ComplianceCheckResult{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", scanName, customRuleName),
			Namespace: testNamespace,
		},
		ID:     customRuleName,
		Status: compv1alpha1.CheckResultFail,
	}

	err = f.AssertHasCheck(suiteName, scanName, expectedCheck)
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}

	t.Logf("Created pod without label that should be ignored: %s", ignoredPod.Name)
	t.Log("Test completed successfully. CustomRule correctly:")
	t.Log("  - Identified non-compliant pod with the test label")
	t.Log("  - Ignored pods without the test label")
	t.Logf("  - Validated that rule %s has FAIL status", customRuleName)
}

func TestCustomRuleWithMultipleInputs(t *testing.T) {
	t.Parallel()
	f := framework.Global

	testName := framework.GetObjNameFromTest(t)
	customRuleName := fmt.Sprintf("%s-network-policy", testName)
	tpName := fmt.Sprintf("%s-tp", testName)
	ssbName := fmt.Sprintf("%s-ssb", testName)
	testNamespace := f.OperatorNamespace

	// Create test namespace without network policies
	testNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-test", testName),
		},
	}

	err := f.Client.Create(context.TODO(), testNs, nil)
	if err != nil {
		t.Fatalf("Failed to create test namespace: %v", err)
	}
	defer f.Client.Delete(context.TODO(), testNs)

	// Create CustomRule that checks for network policies in namespaces
	customRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      customRuleName,
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          customRuleName,
				Title:       "Namespaces Must Have Network Policies",
				Description: "Ensures all namespaces have at least one network policy",
				Severity:    "medium",
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL,
				Expression: `
					namespaces.items.all(ns,
						ns.metadata.name.startsWith("kube-") ||
						ns.metadata.name == "default" ||
						ns.metadata.name.startsWith("openshift") ||
						networkpolicies.items.exists(np,
							np.metadata.namespace == ns.metadata.name
						)
					)
				`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "namespaces",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "namespaces",
						},
					},
					{
						Name: "networkpolicies",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							Group:      "networking.k8s.io",
							APIVersion: "v1",
							Resource:   "networkpolicies",
						},
					},
				},
				FailureReason: "Namespace(s) found without network policies",
			},
		},
	}

	err = f.Client.Create(context.TODO(), customRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), customRule)

	// Wait for CustomRule to be validated and ready
	err = f.WaitForCustomRuleStatus(testNamespace, customRuleName, "Ready")
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}

	// Create TailoredProfile with CustomRule
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: testNamespace,
			Annotations: map[string]string{
				compv1alpha1.DisableOutdatedReferenceValidation: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Network Policy Compliance",
			Description: "Test profile for network policy compliance",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      customRuleName,
					Kind:      "CustomRule",
					Rationale: "All namespaces should have network policies for security",
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatalf("Failed to create TailoredProfile: %v", err)
	}
	defer f.Client.Delete(context.TODO(), tp)

	// Create ScanSettingBinding
	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ssbName,
			Namespace: testNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     tpName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}

	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatalf("Failed to create ScanSettingBinding: %v", err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// Wait for ScanSettingBinding to become ready
	err = f.WaitForScanSettingBindingStatus(testNamespace, ssbName, compv1alpha1.ScanSettingBindingPhaseReady)
	if err != nil {
		t.Fatalf("Failed waiting for ScanSettingBinding to become ready: %v", err)
	}
	t.Logf("ScanSettingBinding %s is now ready", ssbName)

	// Wait for suite to be created and for scans to complete
	suiteName := ssbName

	// Wait for scans to complete
	err = f.WaitForSuiteScansStatus(testNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatalf("Failed waiting for suite scans to complete: %v", err)
	}

	// Validate that the CustomRule result is FAIL
	// For TailoredProfiles, the scan name is the TailoredProfile name
	scanName := tpName
	expectedCheck := compv1alpha1.ComplianceCheckResult{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", scanName, customRuleName),
			Namespace: testNamespace,
		},
		ID:     customRuleName,
		Status: compv1alpha1.CheckResultFail,
	}

	err = f.AssertHasCheck(suiteName, scanName, expectedCheck)
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}

	t.Log("CustomRule with multiple inputs test completed successfully.")
	t.Logf("  - Validated that rule %s has FAIL status", customRuleName)
}

func TestCustomRuleValidation(t *testing.T) {
	t.Parallel()
	f := framework.Global

	testName := framework.GetObjNameFromTest(t)
	testNamespace := f.OperatorNamespace

	// Test 1: Invalid CEL expression
	invalidRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-invalid", testName),
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          fmt.Sprintf("%s-invalid", testName),
				Title:       "Invalid Rule",
				Description: "This rule has invalid CEL expression",
				Severity:    "low",
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL,
				Expression: `
					pods.items.all(pod,
						invalid_function_that_doesnt_exist(pod)
					)
				`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "pods",
						},
					},
				},
				FailureReason: "This should fail validation",
			},
		},
	}

	err := f.Client.Create(context.TODO(), invalidRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), invalidRule)

	// Wait and expect the rule to have Error status
	err = f.WaitForCustomRuleStatus(testNamespace, fmt.Sprintf("%s-invalid", testName), "Error")
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}
	t.Log("CustomRule validation correctly rejected invalid expression")

	// Test 2: Rule with undeclared variable
	undeclaredVarRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-undeclared", testName),
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          fmt.Sprintf("%s-undeclared", testName),
				Title:       "Undeclared Variable Rule",
				Description: "This rule uses undeclared variables",
				Severity:    "low",
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL,
				Expression: `
					pods.items.all(pod,
						deployments.items.exists(d, d.metadata.name == pod.metadata.name)
					)
				`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "pods",
						},
					},
					// 'deployments' is used but not declared as input
				},
				FailureReason: "This should fail validation due to undeclared variable",
			},
		},
	}

	err = f.Client.Create(context.TODO(), undeclaredVarRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), undeclaredVarRule)

	// Wait and expect the rule to have Error status
	err = f.WaitForCustomRuleStatus(testNamespace, fmt.Sprintf("%s-undeclared", testName), "Error")
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}
	t.Log("CustomRule validation correctly detected undeclared variable")

}

func TestCustomRuleCheckTypeAndScannerTypeValidation(t *testing.T) {
	t.Parallel()
	f := framework.Global

	testName := framework.GetObjNameFromTest(t)
	testNamespace := f.OperatorNamespace

	// Test 1: Invalid checkType (should be Platform only)
	invalidCheckTypeRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-invalid-checktype", testName),
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          fmt.Sprintf("%s-invalid-checktype", testName),
				Title:       "Invalid CheckType Rule",
				Description: "This rule has invalid checkType",
				Severity:    "low",
				CheckType:   "Node", // This should be rejected
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL,
				Expression:  `pods.items.size() >= 0`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "pods",
						},
					},
				},
				FailureReason: "This should fail validation due to invalid checkType",
			},
		},
	}

	err := f.Client.Create(context.TODO(), invalidCheckTypeRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), invalidCheckTypeRule)

	// Wait and expect the rule to have Error status
	err = f.WaitForCustomRuleStatus(testNamespace, fmt.Sprintf("%s-invalid-checktype", testName), "Error")
	if err != nil {
		t.Fatalf("CustomRule validation should have failed for invalid checkType: %v", err)
	}
	t.Log("CustomRule validation correctly rejected invalid checkType")

	// Test 2: Invalid scannerType (should be CEL only)
	invalidScannerTypeRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-invalid-scannertype", testName),
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          fmt.Sprintf("%s-invalid-scannertype", testName),
				Title:       "Invalid ScannerType Rule",
				Description: "This rule has invalid scannerType",
				Severity:    "low",
				CheckType:   "Platform", // Valid checkType
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeOpenSCAP, // This should be rejected
				Expression:  `pods.items.size() >= 0`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "pods",
						},
					},
				},
				FailureReason: "This should fail validation due to invalid scannerType",
			},
		},
	}

	err = f.Client.Create(context.TODO(), invalidScannerTypeRule, nil)
	if err == nil {
		t.Fatalf("we should not be able to create a CustomRule with an invalid scannerType")
	}

	t.Log("CustomRule validation correctly rejected invalid scannerType")

	// Test 3: Valid CustomRule with Platform checkType and CEL scannerType
	validRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-valid", testName),
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          fmt.Sprintf("%s-valid", testName),
				Title:       "Valid Rule",
				Description: "This rule has valid checkType and scannerType",
				Severity:    "low",
				CheckType:   "Platform", // Valid checkType
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL, // Valid scannerType
				Expression:  `pods.items.size() >= 0`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "pods",
						},
					},
				},
				FailureReason: "This should pass validation",
			},
		},
	}

	err = f.Client.Create(context.TODO(), validRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), validRule)

	// Wait and expect the rule to have Ready status
	err = f.WaitForCustomRuleStatus(testNamespace, fmt.Sprintf("%s-valid", testName), "Ready")
	if err != nil {
		t.Fatalf("CustomRule validation should have passed for valid rule: %v", err)
	}
	t.Log("CustomRule validation correctly accepted valid checkType and scannerType")

	// Test 4: Valid CustomRule with empty checkType (should default to Platform)
	validEmptyCheckTypeRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-valid-empty-checktype", testName),
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          fmt.Sprintf("%s-valid-empty-checktype", testName),
				Title:       "Valid Empty CheckType Rule",
				Description: "This rule has empty checkType which should be valid",
				Severity:    "low",
				// CheckType is empty, which should be valid
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL, // Valid scannerType
				Expression:  `pods.items.size() >= 0`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "pods",
						},
					},
				},
				FailureReason: "This should pass validation with empty checkType",
			},
		},
	}

	err = f.Client.Create(context.TODO(), validEmptyCheckTypeRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), validEmptyCheckTypeRule)

	// Wait and expect the rule to have Ready status
	err = f.WaitForCustomRuleStatus(testNamespace, fmt.Sprintf("%s-valid-empty-checktype", testName), "Ready")
	if err != nil {
		t.Fatalf("CustomRule validation should have passed for rule with empty checkType: %v", err)
	}
	t.Log("CustomRule validation correctly accepted empty checkType")
}

func TestTailoredProfileRejectsMixedRuleTypes(t *testing.T) {
	t.Parallel()
	f := framework.Global

	testName := framework.GetObjNameFromTest(t)
	testNamespace := f.OperatorNamespace
	customRuleName := fmt.Sprintf("%s-custom", testName)
	tpName := fmt.Sprintf("%s-tp-mixed", testName)
	expression := `pods.items.all(pod, pod.spec.containers.all(container, !has(container.securityContext) || !has(container.securityContext.privileged) || container.securityContext.privileged == false ))`
	// Step 1: Create a valid CustomRule
	customRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      customRuleName,
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          customRuleName,
				Title:       "No Privileged Containers",
				Description: "Ensures no containers are running in privileged mode",
				Severity:    "high",
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL,
				Expression:  expression,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "pods",
						},
					},
				},
				FailureReason: "Privileged container(s) found",
			},
		},
	}

	err := f.Client.Create(context.TODO(), customRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), customRule)

	// Wait for CustomRule to be validated and ready
	err = f.WaitForCustomRuleStatus(testNamespace, customRuleName, "Ready")
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}
	t.Logf("CustomRule %s is ready", customRuleName)

	// Step 2: Create TailoredProfile that mixes CustomRules and regular Rules
	// This should fail validation
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: testNamespace,
			Annotations: map[string]string{
				compv1alpha1.DisableOutdatedReferenceValidation: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Mixed Rule Types Test",
			Description: "This profile incorrectly mixes CustomRules and regular Rules",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					// CustomRule - CEL-based
					Name:      customRuleName,
					Kind:      "CustomRule",
					Rationale: "Ensure containers are not privileged",
				},
				{
					// Regular Rule - OpenSCAP-based
					Name:      "ocp4-cluster-version-operator-exists",
					Kind:      "Rule",
					Rationale: "Make sure cluster version operator exists",
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatalf("Failed to create TailoredProfile: %v", err)
	}
	defer f.Client.Delete(context.TODO(), tp)

	// Step 3: Wait for TailoredProfile to be in Error state
	err = f.WaitForTailoredProfileStatus(testNamespace, tpName, compv1alpha1.TailoredProfileStateError)
	if err != nil {
		t.Fatalf("TailoredProfile did not enter Error state: %v", err)
	}
	t.Logf("TailoredProfile %s is in Error state as expected", tpName)

	// Step 4: Verify the error message
	tpWithError := &compv1alpha1.TailoredProfile{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: tpName, Namespace: testNamespace}, tpWithError)
	if err != nil {
		t.Fatalf("Failed to get TailoredProfile: %v", err)
	}

	expectedErrorContent := "cannot mix CustomRules and regular Rules"
	if !strings.Contains(tpWithError.Status.ErrorMessage, expectedErrorContent) {
		t.Fatalf("Expected error message to contain '%s', but got: %s", expectedErrorContent, tpWithError.Status.ErrorMessage)
	}
	t.Logf("Error message correctly indicates mixed rule types: %s", tpWithError.Status.ErrorMessage)

	// Step 5: Create a TailoredProfile with only CustomRules (should work)
	tpValidName := fmt.Sprintf("%s-tp-valid", testName)
	tpValid := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpValidName,
			Namespace: testNamespace,
			Annotations: map[string]string{
				compv1alpha1.DisableOutdatedReferenceValidation: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "CustomRules Only Test",
			Description: "This profile correctly uses only CustomRules",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      customRuleName,
					Kind:      "CustomRule",
					Rationale: "Ensure containers are not privileged",
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), tpValid, nil)
	if err != nil {
		t.Fatalf("Failed to create valid TailoredProfile: %v", err)
	}
	defer f.Client.Delete(context.TODO(), tpValid)

	// Should be ready since it only has CustomRules
	err = f.WaitForTailoredProfileStatus(testNamespace, tpValidName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatalf("Valid TailoredProfile did not become ready: %v", err)
	}
	t.Logf("TailoredProfile %s with only CustomRules is ready as expected", tpValidName)

	// Step 6: Create a TailoredProfile with only regular Rules (should work)
	tpRegularName := fmt.Sprintf("%s-tp-regular", testName)
	tpRegular := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpRegularName,
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Regular Rules Only Test",
			Description: "This profile correctly uses only regular Rules",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "ocp4-cluster-version-operator-exists",
					Rationale: "Make sure cluster version operator exists",
				},
				{
					Name:      "ocp4-kubeadmin-removed",
					Kind:      "Rule", // Explicitly set Kind to Rule
					Rationale: "Ensure kubeadmin user has been removed",
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), tpRegular, nil)
	if err != nil {
		t.Fatalf("Failed to create regular TailoredProfile: %v", err)
	}
	defer f.Client.Delete(context.TODO(), tpRegular)

	// Should be ready since it only has regular Rules
	err = f.WaitForTailoredProfileStatus(testNamespace, tpRegularName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatalf("Regular TailoredProfile did not become ready: %v", err)
	}
	t.Logf("TailoredProfile %s with only regular Rules is ready as expected", tpRegularName)

	// Step 7: Test updating from valid to invalid (adding a different rule type)
	// Get the valid CustomRule-only profile
	tpToUpdate := &compv1alpha1.TailoredProfile{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: tpValidName, Namespace: testNamespace}, tpToUpdate)
	if err != nil {
		t.Fatalf("Failed to get TailoredProfile for update: %v", err)
	}

	// Update to add a regular Rule, making it invalid
	tpToUpdateCopy := tpToUpdate.DeepCopy()
	tpToUpdateCopy.Spec.EnableRules = append(tpToUpdateCopy.Spec.EnableRules, compv1alpha1.RuleReferenceSpec{
		Name:      "ocp4-cluster-version-operator-exists",
		Kind:      "Rule",
		Rationale: "Adding regular rule to make it invalid",
	})

	err = f.Client.Update(context.TODO(), tpToUpdateCopy)
	if err != nil {
		t.Fatalf("Failed to update TailoredProfile: %v", err)
	}

	// Should go to Error state
	err = f.WaitForTailoredProfileStatus(testNamespace, tpValidName, compv1alpha1.TailoredProfileStateError)
	if err != nil {
		t.Fatalf("Updated TailoredProfile did not enter Error state: %v", err)
	}
	t.Logf("TailoredProfile %s correctly went to Error state after adding mixed rule types", tpValidName)

	t.Log("TestTailoredProfileRejectsMixedRuleTypes completed successfully")
}

func TestCustomRuleFailureReasonInCheckResult(t *testing.T) {
	t.Parallel()
	f := framework.Global

	testName := framework.GetObjNameFromTest(t)
	testNamespace := f.OperatorNamespace
	customRuleName := fmt.Sprintf("%s-replica-check", testName)
	tpName := fmt.Sprintf("%s-tp", testName)
	ssbName := fmt.Sprintf("%s-ssb", testName)

	// Create a CustomRule that will intentionally fail with a specific failure reason
	customRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      customRuleName,
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          fmt.Sprintf("custom_%s", customRuleName),
				Title:       "Ensure Deployments Have at Least 3 Replicas",
				Description: "Validates that all deployments have at least 3 replicas for high availability",
				Severity:    "medium",
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL,
				Expression: `
					deployments.items.all(deployment,
						deployment.spec.replicas >= 3
					)
				`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "deployments",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							Group:             "apps",
							APIVersion:        "v1",
							Resource:          "deployments",
							ResourceNamespace: testNamespace,
						},
					},
				},
				FailureReason: "One or more deployments have less than 3 replicas, which violates the high availability requirement",
			},
		},
	}

	err := f.Client.Create(context.TODO(), customRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), customRule)

	// Wait for CustomRule to be validated and ready
	err = f.WaitForCustomRuleStatus(testNamespace, customRuleName, "Ready")
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}
	t.Logf("CustomRule %s is ready", customRuleName)

	// Create TailoredProfile with the CustomRule
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: testNamespace,
			Annotations: map[string]string{
				compv1alpha1.DisableOutdatedReferenceValidation: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Test Failure Reason",
			Description: "Test that FailureReason appears in ComplianceCheckResult",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      customRuleName,
					Kind:      "CustomRule",
					Rationale: "Testing failure reason propagation",
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatalf("Failed to create TailoredProfile: %v", err)
	}
	defer f.Client.Delete(context.TODO(), tp)

	// Wait for TailoredProfile to be ready
	err = f.WaitForTailoredProfileStatus(testNamespace, tpName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatalf("TailoredProfile failed to become ready: %v", err)
	}
	t.Logf("TailoredProfile %s is ready", tpName)

	// Create ScanSettingBinding
	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ssbName,
			Namespace: testNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     tpName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}

	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatalf("Failed to create ScanSettingBinding: %v", err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// Wait for scans to complete
	// The scan should be NON-COMPLIANT because the compliance-operator deployment likely has only 1 replica
	suiteName := ssbName
	err = f.WaitForSuiteScansStatus(testNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		// It might be compliant if there are 3+ replicas, which is okay for this test
		// We just need to check that the scan completed
		err = f.WaitForSuiteScansStatus(testNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
		if err != nil {
			t.Fatalf("Scan did not complete: %v", err)
		}
		t.Log("Scan completed as compliant (deployments have 3+ replicas)")
	} else {
		t.Log("Scan completed as non-compliant (some deployments have <3 replicas)")

		// Get the ComplianceCheckResult and verify the FailureReason appears in warnings
		checkResultName := fmt.Sprintf("%s-%s", tpName, strings.ReplaceAll(fmt.Sprintf("custom_%s", customRuleName), "_", "-"))
		checkResult := &compv1alpha1.ComplianceCheckResult{}
		err = f.Client.Get(context.TODO(), types.NamespacedName{
			Name:      checkResultName,
			Namespace: testNamespace,
		}, checkResult)
		if err != nil {
			t.Fatalf("Failed to get ComplianceCheckResult: %v", err)
		}

		// Verify the check failed
		if checkResult.Status != compv1alpha1.CheckResultFail {
			t.Logf("Check result status is %s, not FAIL - deployments might have 3+ replicas", checkResult.Status)
		} else {
			// Verify the FailureReason appears in the warnings
			expectedFailureReason := "One or more deployments have less than 3 replicas, which violates the high availability requirement"
			found := false
			for _, warning := range checkResult.Warnings {
				if warning == expectedFailureReason {
					found = true
					break
				}
			}

			if !found {
				t.Fatalf("Expected FailureReason not found in warnings. Warnings: %v", checkResult.Warnings)
			}
			t.Logf("FailureReason correctly appears in ComplianceCheckResult warnings: %s", expectedFailureReason)
		}
	}

	// Create a deployment with only 1 replica to ensure the rule fails
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-test-deployment", testName),
			Namespace: testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: func() *int32 { i := int32(1); return &i }(),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": fmt.Sprintf("%s-test", testName),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": fmt.Sprintf("%s-test", testName),
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "test-container",
							Image:   "busybox:latest",
							Command: []string{"sh", "-c", "sleep 3600"},
						},
					},
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), deployment, nil)
	if err != nil {
		t.Fatalf("Failed to create test deployment: %v", err)
	}
	defer f.Client.Delete(context.TODO(), deployment)

	// Re-run the scan to ensure it fails
	suite := &compv1alpha1.ComplianceSuite{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: suiteName, Namespace: testNamespace}, suite)
	if err != nil {
		t.Fatalf("Failed to get ComplianceSuite: %v", err)
	}

	// Annotate all ComplianceScans in the suite to generate fresh results
	err = f.RescanSuite(suiteName, testNamespace)
	if err != nil {
		t.Fatalf("Failed to rescan ComplianceSuite: %v", err)
	}

	// Wait for the new scan to complete
	err = f.WaitForSuiteScansStatus(testNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatalf("Re-run scan did not complete as non-compliant: %v", err)
	}

	// Get the ComplianceCheckResult again and verify the FailureReason
	checkResultName := fmt.Sprintf("%s-%s", tpName, strings.ReplaceAll(fmt.Sprintf("custom_%s", customRuleName), "_", "-"))
	checkResult := &compv1alpha1.ComplianceCheckResult{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{
		Name:      checkResultName,
		Namespace: testNamespace,
	}, checkResult)
	if err != nil {
		t.Fatalf("Failed to get ComplianceCheckResult after re-run: %v", err)
	}

	// Verify the check failed
	if checkResult.Status != compv1alpha1.CheckResultFail {
		t.Fatalf("Expected check result status to be FAIL but got %s", checkResult.Status)
	}

	// Verify the FailureReason appears in the warnings
	expectedFailureReason := "One or more deployments have less than 3 replicas, which violates the high availability requirement"
	found := false
	for _, warning := range checkResult.Warnings {
		if warning == expectedFailureReason {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("Expected FailureReason not found in warnings after re-run. Warnings: %v", checkResult.Warnings)
	}

	t.Logf("FailureReason correctly appears in ComplianceCheckResult warnings: %s", expectedFailureReason)
	t.Log("TestCustomRuleFailureReasonInCheckResult completed successfully")
}

func TestCustomRuleCascadingStatusUpdate(t *testing.T) {
	t.Parallel()
	f := framework.Global

	testName := framework.GetObjNameFromTest(t)
	testNamespace := f.OperatorNamespace
	customRuleName := fmt.Sprintf("%s-cel", testName)
	tpName := fmt.Sprintf("%s-tp", testName)
	ssbName := fmt.Sprintf("%s-ssb", testName)

	// Step 1: Create a valid CustomRule
	validRule := &compv1alpha1.CustomRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      customRuleName,
			Namespace: testNamespace,
		},
		Spec: compv1alpha1.CustomRuleSpec{
			RulePayload: compv1alpha1.RulePayload{
				ID:          customRuleName,
				Title:       "Pods Must Have Security Context",
				Description: "Ensures all pods have security context defined with runAsNonRoot set to true",
				Severity:    "medium",
			},
			CustomRulePayload: compv1alpha1.CustomRulePayload{
				ScannerType: compv1alpha1.ScannerTypeCEL,
				Expression: `pods.items.all(pod, pod.spec.securityContext != null && pod.spec.securityContext.runAsNonRoot == true)
				`,
				Inputs: []compv1alpha1.InputPayload{
					{
						Name: "pods",
						KubernetesInputSpec: compv1alpha1.KubernetesInputSpec{
							APIVersion: "v1",
							Resource:   "pods",
						},
					},
				},
				FailureReason: "Pod(s) found without resource limits",
			},
		},
	}

	err := f.Client.Create(context.TODO(), validRule, nil)
	if err != nil {
		t.Fatalf("Failed to create CustomRule: %v", err)
	}
	defer f.Client.Delete(context.TODO(), validRule)

	// Wait for CustomRule to be validated and ready
	err = f.WaitForCustomRuleStatus(testNamespace, customRuleName, "Ready")
	if err != nil {
		t.Fatalf("CustomRule validation failed: %v", err)
	}
	t.Logf("CustomRule %s is ready", customRuleName)

	// Step 2: Create TailoredProfile with CustomRule
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: testNamespace,
			Annotations: map[string]string{
				compv1alpha1.DisableOutdatedReferenceValidation: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "Cascading Test Profile",
			Description: "Test profile for cascading status updates",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      customRuleName,
					Kind:      "CustomRule",
					Rationale: "Pods should have resource limits for stability",
				},
			},
		},
	}

	err = f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatalf("Failed to create TailoredProfile: %v", err)
	}
	defer f.Client.Delete(context.TODO(), tp)

	// Wait for TailoredProfile to be ready
	err = f.WaitForTailoredProfileStatus(testNamespace, tpName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatalf("TailoredProfile failed to become ready: %v", err)
	}
	t.Logf("TailoredProfile %s is ready", tpName)

	// Step 3: Create ScanSettingBinding
	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ssbName,
			Namespace: testNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     tpName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}

	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatalf("Failed to create ScanSettingBinding: %v", err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// Wait for ScanSettingBinding to become ready
	err = f.WaitForScanSettingBindingStatus(testNamespace, ssbName, compv1alpha1.ScanSettingBindingPhaseReady)
	if err != nil {
		t.Fatalf("Failed waiting for ScanSettingBinding to become ready: %v", err)
	}
	t.Logf("ScanSettingBinding %s is ready", ssbName)

	// Step 4: Update CustomRule with invalid expression
	t.Log("Updating CustomRule with invalid expression")

	// Fetch the current rule
	currentRule := &compv1alpha1.CustomRule{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: customRuleName, Namespace: testNamespace}, currentRule)
	if err != nil {
		t.Fatalf("Failed to get CustomRule: %v", err)
	}

	// Update with invalid expression
	currentRule.Spec.CustomRulePayload.Expression = `podsx.items.all(pod, pod.spec.securityContext != null && pod.spec.securityContext.runAsNonRoot == true)`

	err = f.Client.Update(context.TODO(), currentRule)
	if err != nil {
		t.Fatalf("Failed to update CustomRule: %v", err)
	}

	// Step 5: Wait for cascading error states
	t.Log("Waiting for cascading error states...")

	// CustomRule should go to Error state
	err = f.WaitForCustomRuleStatus(testNamespace, customRuleName, "Error")
	if err != nil {
		t.Fatalf("CustomRule did not enter Error state: %v", err)
	}
	t.Logf("CustomRule %s is now in Error state", customRuleName)

	// TailoredProfile should go to Error state
	err = f.WaitForTailoredProfileStatus(testNamespace, tpName, compv1alpha1.TailoredProfileStateError)
	if err != nil {
		t.Fatalf("TailoredProfile did not enter Error state: %v", err)
	}
	t.Logf("TailoredProfile %s is now in Error state", tpName)

	// ScanSettingBinding should go to Invalid state
	err = f.WaitForScanSettingBindingStatus(testNamespace, ssbName, compv1alpha1.ScanSettingBindingPhaseInvalid)
	if err != nil {
		t.Fatalf("ScanSettingBinding did not enter Invalid state: %v", err)
	}
	t.Logf("ScanSettingBinding %s is now in Invalid state", ssbName)

	// Step 6: Fix the CustomRule expression
	t.Log("Fixing CustomRule expression")

	// Fetch the current rule again
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: customRuleName, Namespace: testNamespace}, currentRule)
	if err != nil {
		t.Fatalf("Failed to get CustomRule: %v", err)
	}

	// Update with a valid but different expression to ensure change is detected
	currentRule.Spec.CustomRulePayload.Expression = `pods.items.all(pod, pod.spec.securityContext != null && pod.spec.securityContext.runAsNonRoot == true)`

	err = f.Client.Update(context.TODO(), currentRule)
	if err != nil {
		t.Fatalf("Failed to update CustomRule with fix: %v", err)
	}

	// Step 7: Wait for everything to recover
	t.Log("Waiting for resources to recover to good state...")

	// CustomRule should go back to Ready state
	err = f.WaitForCustomRuleStatus(testNamespace, customRuleName, "Ready")
	if err != nil {
		t.Fatalf("CustomRule did not recover to Ready state: %v", err)
	}
	t.Logf("CustomRule %s recovered to Ready state", customRuleName)

	// TailoredProfile should go back to Ready state
	err = f.WaitForTailoredProfileStatus(testNamespace, tpName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatalf("TailoredProfile did not recover to Ready state: %v", err)
	}
	t.Logf("TailoredProfile %s recovered to Ready state", tpName)

	// ScanSettingBinding should go back to Ready state
	err = f.WaitForScanSettingBindingStatus(testNamespace, ssbName, compv1alpha1.ScanSettingBindingPhaseReady)
	if err != nil {
		t.Fatalf("ScanSettingBinding did not recover to Ready state: %v", err)
	}
	t.Logf("ScanSettingBinding %s recovered to Ready state", ssbName)

	t.Log("CustomRule cascading status update test completed successfully")
}

func TestSuiteWithContentThatDoesNotMatch(t *testing.T) {
	t.Parallel()
	f := framework.Global

	pbName := framework.GetObjNameFromTest(t)
	baselineImage := fmt.Sprintf("%s:%s", brokenContentImagePath, "broken_os_detection")
	origPb, err := f.CreateProfileBundle(pbName, baselineImage, framework.RhcosContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle: %s", err)
	}
	// This should get cleaned up at the end of the test
	defer f.Client.Delete(context.TODO(), origPb)
	if err = f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for the ProfileBundle to become available: %s", err)
	}

	suiteName := "test-suite-with-non-matching-content"
	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: fmt.Sprintf("%s-workers-scan", suiteName),
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: baselineImage,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      "ssg-rhcos4-ds.xml",
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							Debug:             true,
							ShowNotApplicable: true,
						},
						NodeSelector: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
			},
		},
	}
	// use Context's create helper to create the object and add a cleanup function for the new object
	err = f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNotApplicable)
	if err != nil {
		t.Fatal(err)
	}
	err = f.SuiteErrorMessageMatchesRegex(f.OperatorNamespace, suiteName, "The suite result is not applicable.*")
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanSettingBinding(t *testing.T) {
	t.Parallel()
	f := framework.Global
	objName := framework.GetObjNameFromTest(t)
	const defaultCpuLimit = "100m"
	const testMemoryLimit = "432Mi"

	rhcosPb := &compv1alpha1.ProfileBundle{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: "rhcos4", Namespace: f.OperatorNamespace}, rhcosPb)
	if err != nil {
		t.Fatalf("unable to get rhcos4 profile bundle required for test: %s", err)
	}

	rhcos4e8profile := &compv1alpha1.Profile{}
	key := types.NamespacedName{Namespace: f.OperatorNamespace, Name: rhcosPb.Name + "-e8"}
	if err := f.Client.Get(context.TODO(), key, rhcos4e8profile); err != nil {
		t.Fatal(err)
	}

	scanSettingName := objName + "-setting"
	scanSetting := compv1alpha1.ScanSetting{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanSettingName,
			Namespace: f.OperatorNamespace,
		},
		ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
			AutoApplyRemediations: false,
		},
		ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
			Debug: true,
			ScanLimits: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceMemory: resource.MustParse(testMemoryLimit),
			},
		},
		Roles: []string{"master", "worker"},
	}

	if err := f.Client.Create(context.TODO(), &scanSetting, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSetting)

	scanSettingBindingName := "generated-suite"
	scanSettingBinding := compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanSettingBindingName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			// TODO: test also OCP profile when it works completely
			{
				Name:     rhcos4e8profile.Name,
				Kind:     "Profile",
				APIGroup: "compliance.openshift.io/v1alpha1",
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			Name:     scanSetting.Name,
			Kind:     "ScanSetting",
			APIGroup: "compliance.openshift.io/v1alpha1",
		},
	}

	if err := f.Client.Create(context.TODO(), &scanSettingBinding, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingBinding)

	// Wait until the suite finishes, thus verifying the suite exists
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, scanSettingBindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatal(err)
	}

	masterScanKey := types.NamespacedName{Namespace: f.OperatorNamespace, Name: rhcos4e8profile.Name + "-master"}
	masterScan := &compv1alpha1.ComplianceScan{}
	if err := f.Client.Get(context.TODO(), masterScanKey, masterScan); err != nil {
		t.Fatal(err)
	}

	if masterScan.Spec.Debug != true {
		log.Println("Expected that the settings set debug to true in master scan")
	}

	workerScanKey := types.NamespacedName{Namespace: f.OperatorNamespace, Name: rhcos4e8profile.Name + "-worker"}
	workerScan := &compv1alpha1.ComplianceScan{}
	if err := f.Client.Get(context.TODO(), workerScanKey, workerScan); err != nil {
		t.Fatal(err)
	}

	if workerScan.Spec.Debug != true {
		log.Println("Expected that the settings set debug to true in workers scan")
	}

	podList := &corev1.PodList{}
	if err := f.Client.List(context.TODO(), podList, client.InNamespace(f.OperatorNamespace), client.MatchingLabels(map[string]string{
		"workload": "scanner",
	})); err != nil {
		t.Fatal(err)
	}
	// check if the scanning pod has properly been created and has priority class set
	for _, pod := range podList.Items {
		if strings.Contains(pod.Name, workerScan.Name) {
			if err := framework.WaitForPod(framework.CheckPodLimit(f.KubeClient, pod.Name, f.OperatorNamespace, defaultCpuLimit, testMemoryLimit)); err != nil {
				t.Fatal(err)
			}
		}
	}

}
func TestScanSettingBindingNoStorage(t *testing.T) {
	t.Parallel()
	f := framework.Global
	objName := framework.GetObjNameFromTest(t)
	var baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "kubeletconfig")
	const requiredRule = "kubelet-eviction-thresholds-set-soft-imagefs-available"
	pbName := framework.GetObjNameFromTest(t)
	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	ocpPb, err := f.CreateProfileBundle(pbName, baselineImage, framework.OcpContentFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ocpPb)
	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatal(err)
	}

	// Check that if the rule we are going to test is there
	requiredRuleName := prefixName(pbName, requiredRule)
	err, found := framework.Global.DoesRuleExist(f.OperatorNamespace, requiredRuleName)
	if err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("Expected rule %s not found", requiredRuleName)
	}

	suiteName := "storage-test-node"
	scanSettingBindingName := suiteName

	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
			Annotations: map[string]string{
				compv1alpha1.DisableOutdatedReferenceValidation: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "storage-test",
			Description: "A test tailored profile to test storage settings",
			ManualRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      prefixName(pbName, requiredRule),
					Rationale: "To be tested",
				},
			},
		},
	}

	createTPErr := f.Client.Create(context.TODO(), tp, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tp)
	scanSettingNoStorageName := objName + "-setting-no-storage"
	falseValue := false
	trueValue := true
	scanSettingWithStorageName := objName + "-setting-with-storage"
	scanSettingNoStorage := compv1alpha1.ScanSetting{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanSettingNoStorageName,
			Namespace: f.OperatorNamespace,
		},
		ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
			AutoApplyRemediations: false,
		},
		ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
			Debug: true,
			RawResultStorage: compv1alpha1.RawResultStorageSettings{
				Enabled: &falseValue,
			},
		},
		Roles: []string{"master", "worker"},
	}

	scanSettingWithStorage := compv1alpha1.ScanSetting{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanSettingWithStorageName,
			Namespace: f.OperatorNamespace,
		},
		ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
			AutoApplyRemediations: false,
		},
		ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
			Debug: true,
			RawResultStorage: compv1alpha1.RawResultStorageSettings{
				Enabled: &trueValue,
			},
		},
		Roles: []string{"master", "worker"},
	}

	if err := f.Client.Create(context.TODO(), &scanSettingNoStorage, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingNoStorage)

	if err := f.Client.Create(context.TODO(), &scanSettingWithStorage, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingWithStorage)

	scanSettingBinding := compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanSettingBindingName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     suiteName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			Name:     scanSettingNoStorage.Name,
			Kind:     "ScanSetting",
			APIGroup: "compliance.openshift.io/v1alpha1",
		},
	}

	if err := f.Client.Create(context.TODO(), &scanSettingBinding, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingBinding)

	// Wait until the suite finishes, thus verifying the suite exists
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, scanSettingBindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatal(err)
	}

	scanKey := types.NamespacedName{Namespace: f.OperatorNamespace, Name: suiteName + "-master"}
	scan := &compv1alpha1.ComplianceScan{}
	if err := f.Client.Get(context.TODO(), scanKey, scan); err != nil {
		t.Fatal(err)
	}

	if scan.Spec.RawResultStorage.Enabled != nil && *scan.Spec.RawResultStorage.Enabled {
		t.Fatal("Expected that the scan does not have raw result storage enabled")
	}

	pvcList := &corev1.PersistentVolumeClaimList{}
	err = f.Client.List(context.TODO(), pvcList, client.InNamespace(f.OperatorNamespace), client.MatchingLabels(map[string]string{
		compv1alpha1.ComplianceScanLabel: scan.Name,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pvcList.Items) > 0 {
		for _, pvc := range pvcList.Items {
			t.Fatalf("Found unexpected PVC %s", pvc.Name)
		}
		t.Fatal("Expected not to find PVC associated with the scan.")
	}
	// let's delete the scan setting binding
	if err := f.Client.Delete(context.TODO(), &scanSettingBinding); err != nil {
		t.Fatal(err)
	}

	// let's create a new scan setting binding with the with storage setting
	scanSettingBinding = compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanSettingBindingName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     suiteName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			Name:     scanSettingWithStorage.Name,
			Kind:     "ScanSetting",
			APIGroup: "compliance.openshift.io/v1alpha1",
		},
	}

	if err := f.Client.Create(context.TODO(), &scanSettingBinding, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingBinding)

	// Wait until the suite finishes, thus verifying the suite exists
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, scanSettingBindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatal(err)
	}

	scan = &compv1alpha1.ComplianceScan{}
	if err := f.Client.Get(context.TODO(), scanKey, scan); err != nil {
		t.Fatal(err)
	}

	if scan.Spec.RawResultStorage.Enabled != nil && *scan.Spec.RawResultStorage.Enabled == false {
		t.Fatal("Expected that the scan has raw result storage enabled")
	}

	pvcList = &corev1.PersistentVolumeClaimList{}
	err = f.Client.List(context.TODO(), pvcList, client.InNamespace(f.OperatorNamespace), client.MatchingLabels(map[string]string{
		compv1alpha1.ComplianceScanLabel: scan.Name,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pvcList.Items) == 0 {
		t.Fatal("Expected to find PVC associated with the scan.")
	}
	t.Logf("Found PVC %s", pvcList.Items[0].Name)
	t.Logf("Succeeded to create PVC associated with the scan.")

	// let's update the scan setting binding to use the no storage setting
	ssb := &compv1alpha1.ScanSettingBinding{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: f.OperatorNamespace, Name: scanSettingBindingName}, ssb); err != nil {
		t.Fatal("Expected to find the scan setting binding, but got error: ", err)
	}
	ssb.SettingsRef.Name = scanSettingNoStorage.Name
	if err := f.Client.Update(context.TODO(), ssb); err != nil {
		t.Fatal(err)
	}

	// let's rerun the scan
	err = f.ReRunScan(scan.Name, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
	// wait for scan to finish
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, scanSettingBindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatal(err)
	}

	// get the scan again
	scan = &compv1alpha1.ComplianceScan{}
	if err := f.Client.Get(context.TODO(), scanKey, scan); err != nil {
		t.Fatal(err)
	}

	// make sure enabled is false
	if scan.Spec.RawResultStorage.Enabled != nil && *scan.Spec.RawResultStorage.Enabled == true {
		t.Fatal("Expected that the scan does not have raw result storage enabled")
	}

	// let's check that the PVC should be there and still associated with the scan
	pvcList = &corev1.PersistentVolumeClaimList{}
	err = f.Client.List(context.TODO(), pvcList, client.InNamespace(f.OperatorNamespace), client.MatchingLabels(map[string]string{
		compv1alpha1.ComplianceScanLabel: scan.Name,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pvcList.Items) == 0 {
		t.Fatal("Expected to find PVC associated with the scan.")
	}
	t.Logf("Found PVC %s", pvcList.Items[0].Name)
	t.Logf("Succeeded to check that the PVC is still there.")

}

func TestScanSettingBindingTailoringManyEnablingRulePass(t *testing.T) {

	t.Parallel()
	f := framework.Global
	const (
		changeTypeRule      = "kubelet-anonymous-auth"
		unChangedTypeRule   = "api-server-insecure-port"
		moderateProfileName = "moderate"
		tpMixName           = "many-migrated-mix-tp"
		tpSingleName        = "migrated-single-tp"
		tpSingleNoPruneName = "migrated-single-no-prune-tp"
	)
	var (
		baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "kubelet_default")
		modifiedImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "new_kubeletconfig")
	)

	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	pbName := framework.GetObjNameFromTest(t)
	origPb, err := f.CreateProfileBundle(pbName, baselineImage, framework.OcpContentFile)
	if err != nil {
		t.Fatalf("failed to create ProfileBundle: %s", err)
	}
	// This should get cleaned up at the end of the test
	defer f.Client.Delete(context.TODO(), origPb)

	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed waiting for the ProfileBundle to become available: %s", err)
	}

	// Check that the rule exists in the original profile and it is a Platform rule
	changeTypeRuleName := prefixName(pbName, changeTypeRule)
	err, found := f.DoesRuleExist(origPb.Namespace, changeTypeRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found != true {
		t.Fatalf("expected rule %s to exist in namespace %s", changeTypeRuleName, origPb.Namespace)
	}
	if err := f.AssertRuleIsPlatformType(changeTypeRuleName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}

	// Check that the rule exists in the original profile and it is a Platform rule
	unChangedTypeRuleName := prefixName(pbName, unChangedTypeRule)
	err, found = f.DoesRuleExist(origPb.Namespace, unChangedTypeRuleName)
	if err != nil {
		t.Fatal(err)
	} else if found != true {
		t.Fatalf("expected rule %s to exist in namespace %s", unChangedTypeRuleName, origPb.Namespace)
	}
	if err := f.AssertRuleIsPlatformType(unChangedTypeRuleName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}

	tpMix := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpMixName,
			Namespace: f.OperatorNamespace,
			Annotations: map[string]string{
				compv1alpha1.PruneOutdatedReferencesAnnotationKey: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "TestForManyRules",
			Description: "TestForManyRules",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      changeTypeRuleName,
					Rationale: "this rule should be removed from the profile",
				},
				{
					Name:      unChangedTypeRuleName,
					Rationale: "this rule should not be removed from the profile",
				},
			},
		},
	}

	tpSingle := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpSingleName,
			Namespace: f.OperatorNamespace,
			Annotations: map[string]string{
				compv1alpha1.PruneOutdatedReferencesAnnotationKey: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "TestForManyRules",
			Description: "TestForManyRules",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      changeTypeRuleName,
					Rationale: "this rule should be removed from the profile",
				},
			},
		},
	}

	tpMixNoPrune := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpSingleNoPruneName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "TestForNoPrune",
			Description: "TestForNoPrune",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      changeTypeRuleName,
					Rationale: "this rule should not be removed from the profile",
				},
				{
					Name:      unChangedTypeRuleName,
					Rationale: "this rule should not be removed from the profile",
				},
			},
		},
	}

	createTPErr := f.Client.Create(context.TODO(), tpMix, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tpMix)
	// check the status of the TP to make sure it has no errors
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpMixName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	hasRule, err := f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpMixName, changeTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule {
		t.Fatalf("Expected the tailored profile to have rule: %s", changeTypeRuleName)
	}

	hasRule, err = f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpMixName, unChangedTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule {
		t.Fatalf("Expected the tailored profile to have rule: %s", unChangedTypeRuleName)
	}

	// tpSingle test
	createTPErr = f.Client.Create(context.TODO(), tpSingle, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tpSingle)
	// check the status of the TP to make sure it has no errors
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpSingleName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	hasRule, err = f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpSingleName, changeTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule {
		t.Fatalf("Expected the tailored profile to have rule: %s", changeTypeRuleName)
	}

	// tpMixNoPrune test
	createTPErr = f.Client.Create(context.TODO(), tpMixNoPrune, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tpMixNoPrune)
	// check the status of the TP to make sure it has no errors
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpSingleNoPruneName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	hasRule, err = f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpSingleNoPruneName, changeTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}

	if !hasRule {
		t.Fatalf("Expected the tailored profile to have rule: %s", changeTypeRuleName)
	}

	// update the image with a new hash
	modPb := origPb.DeepCopy()
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Namespace: modPb.Namespace, Name: modPb.Name}, modPb); err != nil {
		t.Fatalf("failed to get ProfileBundle %s", modPb.Name)
	}

	modPb.Spec.ContentImage = modifiedImage
	if err := f.Client.Update(context.TODO(), modPb); err != nil {
		t.Fatalf("failed to update ProfileBundle %s: %s", modPb.Name, err)
	}

	// Wait for the update to happen, the PB will flip first to pending, then to valid
	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatalf("failed to parse ProfileBundle %s: %s", pbName, err)
	}

	// Make sure the rules parsed correctly and one did indeed change from
	// a Platform to a Node rule. Note that this switch didn't happen
	// because of the test, but how the data stream was built.
	if err := f.AssertProfileBundleMustHaveParsedRules(pbName); err != nil {
		t.Fatal(err)
	}
	if err := f.AssertRuleIsPlatformType(unChangedTypeRuleName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}
	if err := f.AssertRuleIsNodeType(changeTypeRuleName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}

	// Assert the kubelet-anonymous-auth rule switched from Platform to Node type
	if err := f.AssertRuleCheckTypeChangedAnnotationKey(f.OperatorNamespace, changeTypeRuleName, "Platform"); err != nil {
		t.Fatal(err)
	}

	// check that the tp has been updated with the removed rule mixTP
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpMixName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	hasRule, err = f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpMixName, changeTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}
	if hasRule {
		t.Fatal("Expected the tailored profile to not have the rule")
	}

	hasRule, err = f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpMixName, unChangedTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule {
		t.Fatalf("Expected the tailored profile to have rule: %s", unChangedTypeRuleName)
	}

	// check that the tp has been updated with the removed rule singleTP
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpSingleName, compv1alpha1.TailoredProfileStateError)
	if err != nil {
		t.Fatal(err)
	}

	hasRule, err = f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpSingleName, changeTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}
	if hasRule {
		t.Fatalf("Expected the tailored profile not to have rule: %s", changeTypeRuleName)
	}

	// check that the no prune tp still has the rule
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpSingleNoPruneName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	hasRule, err = f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpSingleNoPruneName, changeTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule {
		t.Fatalf("Expected the tailored profile to have rule: %s", changeTypeRuleName)
	}

	// check that we have a warning message in the tailored profile
	tpSingleNoPruneFetched := &compv1alpha1.TailoredProfile{}
	key := types.NamespacedName{Namespace: f.OperatorNamespace, Name: tpSingleNoPruneName}
	if err := f.Client.Get(context.Background(), key, tpSingleNoPruneFetched); err != nil {
		t.Fatal(err)
	}

	if len(tpSingleNoPruneFetched.Status.Warnings) == 0 {
		t.Fatalf("Expected the tailored profile to have a warning message but got none")
	}

	// check that the warning message is about the rule
	if !strings.Contains(tpSingleNoPruneFetched.Status.Warnings, changeTypeRule) {
		t.Fatalf("Expected the tailored profile to have a warning message about migrated rule: %s but got: %s", changeTypeRule, tpSingleNoPruneFetched.Status.Warnings)
	}

	// Annotate the TP to prune outdated references
	tpSingleNoPruneFetchedCopy := tpSingleNoPruneFetched.DeepCopy()
	tpSingleNoPruneFetchedCopy.Annotations[compv1alpha1.PruneOutdatedReferencesAnnotationKey] = "true"
	if err := f.Client.Update(context.Background(), tpSingleNoPruneFetchedCopy); err != nil {
		t.Fatal(err)
	}

	// check that the warning message is gone when we prune outdated references
	err = f.WaitForTailoredProfileStatus(f.OperatorNamespace, tpSingleNoPruneName, compv1alpha1.TailoredProfileStateReady)
	if err != nil {
		t.Fatal(err)
	}

	tpSingleNoPruneNoWarning := &compv1alpha1.TailoredProfile{}
	key = types.NamespacedName{Namespace: f.OperatorNamespace, Name: tpSingleNoPruneName}
	if err := f.Client.Get(context.Background(), key, tpSingleNoPruneFetched); err != nil {
		t.Fatal(err)
	}

	if len(tpSingleNoPruneNoWarning.Status.Warnings) != 0 {
		t.Fatalf("Expected the tailored profile to have no warning message but got: %s", tpSingleNoPruneFetched.Status.Warnings)
	}
	// check that the rule is being removed from the profile
	hasRule, err = f.EnableRuleExistInTailoredProfile(f.OperatorNamespace, tpSingleNoPruneName, changeTypeRuleName)
	if err != nil {
		t.Fatal(err)
	}

	if hasRule {
		t.Fatalf("Expected the tailored profile not to have rule: %s", changeTypeRuleName)
	}

}

func TestScanSettingBindingUsesDefaultScanSetting(t *testing.T) {
	t.Parallel()
	f := framework.Global
	objName := framework.GetObjNameFromTest(t)
	scanSettingBindingName := objName + "-binding"
	scanSettingBinding := compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanSettingBindingName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				Name:     "ocp4-cis",
				Kind:     "Profile",
				APIGroup: "compliance.openshift.io/v1alpha1",
			},
		},
	}
	if err := f.Client.Create(context.TODO(), &scanSettingBinding, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingBinding)

	// Wait until the suite finishes
	err := f.WaitForSuiteScansStatus(f.OperatorNamespace, scanSettingBindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatal(err)
	}

	bindingKey := types.NamespacedName{Namespace: f.OperatorNamespace, Name: scanSettingBindingName}
	binding := &compv1alpha1.ScanSettingBinding{}
	if err := f.Client.Get(context.TODO(), bindingKey, binding); err != nil {
		t.Fatal(err)
	}

	// Make sure the binding used the `default` ScanSetting.
	if binding.SettingsRef.Name != "default" {
		t.Fatal("Expected the settings reference to use the default ScanSetting")
	}
}

func TestScanSettingBindingWatchesTailoredProfile(t *testing.T) {
	t.Parallel()
	f := framework.Global
	tpName := framework.GetObjNameFromTest(t)
	bindingName := framework.GetObjNameFromTest(t)

	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "TestScanSettingBindingWatchesTailoredProfile",
			Description: "TestScanSettingBindingWatchesTailoredProfile",
			DisableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      "no-such-rule",
					Rationale: "testing",
				},
			},
			Extends: "ocp4-cis",
		},
	}

	err := f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatal("failed to create tailored profile")
	}
	defer f.Client.Delete(context.TODO(), tp)

	// make sure the TP is created with an error as expected
	err = wait.Poll(framework.RetryInterval, framework.Timeout, func() (bool, error) {
		tpGet := &compv1alpha1.TailoredProfile{}
		getErr := f.Client.Get(context.TODO(), types.NamespacedName{Name: tpName, Namespace: f.OperatorNamespace}, tpGet)
		if getErr != nil {
			// not gettable yet? retry
			return false, nil
		}

		if tpGet.Status.State != compv1alpha1.TailoredProfileStateError {
			return false, errors.New("expected the TP to be created with an error")
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	scanSettingBinding := compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				Name:     bindingName,
				Kind:     "TailoredProfile",
				APIGroup: "compliance.openshift.io/v1alpha1",
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			Name:     "default",
			Kind:     "ScanSetting",
			APIGroup: "compliance.openshift.io/v1alpha1",
		},
	}

	if err := f.Client.Create(context.TODO(), &scanSettingBinding, nil); err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &scanSettingBinding)

	// Wait until the suite binding receives an error condition
	err = wait.Poll(framework.RetryInterval, framework.Timeout, func() (bool, error) {
		ssbGet := &compv1alpha1.ScanSettingBinding{}
		getErr := f.Client.Get(context.TODO(), types.NamespacedName{Name: bindingName, Namespace: f.OperatorNamespace}, ssbGet)
		if getErr != nil {
			// not gettable yet? retry
			return false, nil
		}

		readyCond := ssbGet.Status.Conditions.GetCondition("Ready")
		if readyCond == nil {
			return false, nil
		}
		if readyCond.Status != corev1.ConditionFalse && readyCond.Reason != "Invalid" {
			return false, fmt.Errorf("expected ready=false, reason=invalid, got %v", readyCond)
		}

		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Fix the TP
	tpGet := &compv1alpha1.TailoredProfile{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: tpName, Namespace: f.OperatorNamespace}, tpGet)
	if err != nil {
		t.Fatal(err)
	}

	tpUpdate := tpGet.DeepCopy()
	tpUpdate.Spec.DisableRules = []compv1alpha1.RuleReferenceSpec{
		{
			Name:      "ocp4-file-owner-scheduler-kubeconfig",
			Rationale: "testing",
		},
	}

	err = f.Client.Update(context.TODO(), tpUpdate)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the binding to transition to ready state
	// Wait until the suite binding receives an error condition
	err = wait.Poll(framework.RetryInterval, framework.Timeout, func() (bool, error) {
		ssbGet := &compv1alpha1.ScanSettingBinding{}
		getErr := f.Client.Get(context.TODO(), types.NamespacedName{Name: bindingName, Namespace: f.OperatorNamespace}, ssbGet)
		if getErr != nil {
			// not gettable yet? retry
			return false, nil
		}

		readyCond := ssbGet.Status.Conditions.GetCondition("Ready")
		if readyCond == nil {
			return false, nil
		}
		if readyCond.Status != corev1.ConditionTrue && readyCond.Reason != "Processed" {
			// don't return an error right away, let the poll just fail if it takes too long
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

}

func TestManualRulesTailoredProfile(t *testing.T) {
	t.Parallel()
	f := framework.Global
	var baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "kubeletconfig")
	const requiredRule = "kubelet-eviction-thresholds-set-soft-imagefs-available"
	pbName := framework.GetObjNameFromTest(t)
	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	ocpPb, err := f.CreateProfileBundle(pbName, baselineImage, framework.OcpContentFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ocpPb)
	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatal(err)
	}

	// Check that if the rule we are going to test is there
	requiredRuleName := prefixName(pbName, requiredRule)
	err, found := framework.Global.DoesRuleExist(f.OperatorNamespace, requiredRuleName)
	if err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("Expected rule %s not found", requiredRuleName)
	}

	suiteName := "manual-rules-test-node"
	masterScanName := fmt.Sprintf("%s-master", suiteName)

	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
			Annotations: map[string]string{
				compv1alpha1.DisableOutdatedReferenceValidation: "true",
			},
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "manual-rules-test",
			Description: "A test tailored profile to test manual-rules",
			ManualRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      prefixName(pbName, requiredRule),
					Rationale: "To be tested",
				},
			},
		},
	}

	createTPErr := f.Client.Create(context.TODO(), tp, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tp)

	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     suiteName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}

	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// Ensure that all the scans in the suite have finished and are marked as Done
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
	if err != nil {
		t.Fatal(err)
	}

	// the check should be shown as manual
	checkResult := compv1alpha1.ComplianceCheckResult{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-kubelet-eviction-thresholds-set-soft-imagefs-available", masterScanName),
			Namespace: f.OperatorNamespace,
		},
		ID:       "xccdf_org.ssgproject.content_rule_kubelet_eviction_thresholds_set_soft_imagefs_available",
		Status:   compv1alpha1.CheckResultManual,
		Severity: compv1alpha1.CheckResultSeverityMedium,
	}
	err = f.AssertHasCheck(suiteName, masterScanName, checkResult)
	if err != nil {
		t.Fatal(err)
	}

	inNs := client.InNamespace(f.OperatorNamespace)
	withLabel := client.MatchingLabels{"profile-bundle": pbName}

	remList := &compv1alpha1.ComplianceRemediationList{}
	err = f.Client.List(context.TODO(), remList, inNs, withLabel)
	if err != nil {
		t.Fatal(err)
	}

	if len(remList.Items) != 0 {
		t.Fatal("expected no remediation")
	}
}

func TestHideRule(t *testing.T) {
	t.Parallel()
	f := framework.Global
	var baselineImage = fmt.Sprintf("%s:%s", brokenContentImagePath, "hide_rule")
	const requiredRule = "version-detect"
	pbName := framework.GetObjNameFromTest(t)
	prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

	ocpPb, err := f.CreateProfileBundle(pbName, baselineImage, framework.OcpContentFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ocpPb)
	if err := f.WaitForProfileBundleStatus(pbName, compv1alpha1.DataStreamValid); err != nil {
		t.Fatal(err)
	}

	// Check that if the rule we are going to test is there
	requiredRuleName := prefixName(pbName, requiredRule)
	err, found := f.DoesRuleExist(ocpPb.Namespace, requiredRuleName)
	if err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("Expected rule %s not found", requiredRuleName)
	}

	suiteName := "hide-rules-test"
	scanName := "hide-rules-test"

	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       "hide-rules-test",
			Description: "A test tailored profile to test hide-rules",
			EnableRules: []compv1alpha1.RuleReferenceSpec{
				{
					Name:      prefixName(pbName, requiredRule),
					Rationale: "To be tested",
				},
			},
		},
	}

	createTPErr := f.Client.Create(context.TODO(), tp, nil)
	if createTPErr != nil {
		t.Fatal(createTPErr)
	}
	defer f.Client.Delete(context.TODO(), tp)

	ssb := &compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				APIGroup: "compliance.openshift.io/v1alpha1",
				Kind:     "TailoredProfile",
				Name:     suiteName,
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			APIGroup: "compliance.openshift.io/v1alpha1",
			Kind:     "ScanSetting",
			Name:     "default",
		},
	}

	err = f.Client.Create(context.TODO(), ssb, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), ssb)

	// Ensure that all the scans in the suite have finished and are marked as Done
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNotApplicable)
	if err != nil {
		t.Fatal(err)
	}

	// the check should be shown as manual
	checkResult := compv1alpha1.ComplianceCheckResult{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-version-detect", scanName),
			Namespace: f.OperatorNamespace,
		},
		ID:       "xccdf_org.ssgproject.content_rule_version_detect",
		Status:   compv1alpha1.CheckResultNoResult,
		Severity: compv1alpha1.CheckResultSeverityMedium,
	}
	err = f.AssertHasCheck(suiteName, scanName, checkResult)
	if err == nil {
		t.Fatalf("The check should not be found in the scan %s", scanName)
	}
}

func TestScheduledSuiteTimeoutFail(t *testing.T) {
	t.Parallel()
	f := framework.Global
	suiteName := "test-scheduled-suite-timeout-fail"

	workerScanName := fmt.Sprintf("%s-workers-scan", suiteName)
	selectWorkers := map[string]string{
		"node-role.kubernetes.io/worker": "",
	}

	testSuite := &compv1alpha1.ComplianceSuite{
		ObjectMeta: metav1.ObjectMeta{
			Name:      suiteName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceSuiteSpec{
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations: false,
			},
			Scans: []compv1alpha1.ComplianceScanSpecWrapper{
				{
					Name: workerScanName,
					ComplianceScanSpec: compv1alpha1.ComplianceScanSpec{
						ContentImage: contentImagePath,
						Profile:      "xccdf_org.ssgproject.content_profile_moderate",
						Content:      framework.RhcosContentFile,
						Rule:         "xccdf_org.ssgproject.content_rule_no_netrc_files",
						NodeSelector: selectWorkers,
						ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
							MaxRetryOnTimeout: 0,
							RawResultStorage: compv1alpha1.RawResultStorageSettings{
								Rotation: 1,
							},
							Timeout: "1s",
							Debug:   true,
						},
					},
				},
			},
		},
	}

	err := f.Client.Create(context.TODO(), testSuite, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), testSuite)

	// Ensure that all the scans in the suite have finished and are marked as Done
	err = f.WaitForSuiteScansStatus(f.OperatorNamespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultError)
	if err != nil {
		t.Fatal(err)
	}
	scan := &compv1alpha1.ComplianceScan{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: workerScanName, Namespace: f.OperatorNamespace}, scan)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scan.Annotations[compv1alpha1.ComplianceScanTimeoutAnnotation]; !ok {
		t.Fatal("The scan should have the timeout annotation")
	}
}

func TestResultServerHTTPVersion(t *testing.T) {
	t.Parallel()
	f := framework.Global
	endpoints := []string{
		fmt.Sprintf("https://metrics.%s.svc:8585/metrics-co", f.OperatorNamespace),
		fmt.Sprintf("http://metrics.%s.svc:8383/metrics", f.OperatorNamespace),
	}

	expectedHTTPVersion := "HTTP/1.1"
	for _, endpoint := range endpoints {
		err := f.AssertMetricsEndpointUsesHTTPVersion(endpoint, expectedHTTPVersion)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuleHasProfileAnnotation(t *testing.T) {
	t.Parallel()
	f := framework.Global
	const requiredRule = "ocp4-file-groupowner-worker-kubeconfig"
	const expectedRuleProfileAnnotation = "ocp4-pci-dss-node,ocp4-moderate-node,ocp4-stig-node,ocp4-nerc-cip-node,ocp4-cis-node,ocp4-high-node"
	err, found := f.DoesRuleExist(f.OperatorNamespace, requiredRule)
	if err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatalf("Expected rule %s not found", requiredRule)
	}

	// Check if requiredRule has the correct profile annotation
	rule := &compv1alpha1.Rule{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{
		Name:      requiredRule,
		Namespace: f.OperatorNamespace,
	}, rule)
	if err != nil {
		t.Fatal(err)
	}
	expectedProfiles := strings.Split(expectedRuleProfileAnnotation, ",")
	for _, profileName := range expectedProfiles {
		if !f.AssertProfileInRuleAnnotation(rule, profileName) {
			t.Fatalf("expected to find profile %s in rule %s", profileName, rule.Name)
		}
	}
}

func TestScanCleansUpComplianceCheckResults(t *testing.T) {
	f := framework.Global
	t.Parallel()

	tpName := framework.GetObjNameFromTest(t)
	bindingName := tpName + "-binding"

	// create a tailored profile
	tp := &compv1alpha1.TailoredProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tpName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.TailoredProfileSpec{
			Title:       tpName,
			Description: tpName,
			Extends:     "ocp4-cis",
		},
	}

	err := f.Client.Create(context.TODO(), tp, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), tp)

	// run a scan
	ssb := compv1alpha1.ScanSettingBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bindingName,
			Namespace: f.OperatorNamespace,
		},
		Profiles: []compv1alpha1.NamedObjectReference{
			{
				Name:     tpName,
				Kind:     "TailoredProfile",
				APIGroup: "compliance.openshift.io/v1alpha1",
			},
		},
		SettingsRef: &compv1alpha1.NamedObjectReference{
			Name:     "default",
			Kind:     "ScanSetting",
			APIGroup: "compliance.openshift.io/v1alpha1",
		},
	}
	err = f.Client.Create(context.TODO(), &ssb, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Client.Delete(context.TODO(), &ssb)

	if err := f.WaitForSuiteScansStatus(f.OperatorNamespace, bindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant); err != nil {
		t.Fatal(err)
	}

	// verify a compliance check result exists
	checkName := tpName + "-audit-profile-set"
	checkResult := compv1alpha1.ComplianceCheckResult{
		ObjectMeta: metav1.ObjectMeta{
			Name:      checkName,
			Namespace: f.OperatorNamespace,
		},
		ID:       "xccdf_org.ssgproject.content_rule_audit_profile_set",
		Status:   compv1alpha1.CheckResultFail,
		Severity: compv1alpha1.CheckResultSeverityMedium,
	}
	err = f.AssertHasCheck(bindingName, tpName, checkResult)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.AssertRemediationExists(checkName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}

	// update tailored profile to exclude the rule before we kick off another run
	tpGet := &compv1alpha1.TailoredProfile{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: tpName, Namespace: f.OperatorNamespace}, tpGet)
	if err != nil {
		t.Fatal(err)
	}

	tpUpdate := tpGet.DeepCopy()
	ruleName := "ocp4-audit-profile-set"
	tpUpdate.Spec.DisableRules = []compv1alpha1.RuleReferenceSpec{
		{
			Name:      ruleName,
			Rationale: "testing to ensure scan results are cleaned up",
		},
	}

	err = f.Client.Update(context.TODO(), tpUpdate)
	if err != nil {
		t.Fatal(err)
	}

	// rerun the scan
	err = f.ReRunScan(tpName, f.OperatorNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.WaitForSuiteScansStatus(f.OperatorNamespace, bindingName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant); err != nil {
		t.Fatal(err)
	}

	// verify the compliance check result doesn't exist, which will also
	// mean the compliance remediation should also be gone
	if err = f.AssertScanDoesNotContainCheck(tpName, checkName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}
	if err = f.AssertRemediationDoesNotExists(checkName, f.OperatorNamespace); err != nil {
		t.Fatal(err)
	}
}

func TestScanWithoutBundlePassesDeprecationCheck(t *testing.T) {
	t.Parallel()
	f := framework.Global

	scanName := framework.GetObjNameFromTest(t)
	testScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ComplianceScanSpec{
			Profile: "xccdf_org.ssgproject.content_profile_moderate",
			// Make the ProfileBundle lookup fail because the
			// Content and ContentImage mismatch. This means the
			// operator can't check if the profile is deprecated
			// because it can't reliably know which bundle it came
			// from and hasn't parsed that specific datastream. In
			// cases like this, the profile deprecation logic
			// shouldn't prevent the scan. Advanced users might use
			// this technique to point to their own custom content,
			// which is rare but possible.
			Content:      framework.OcpContentFile,
			ContentImage: contentImagePath,
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				Debug: true,
			},
		},
	}

	// Create the scan directly since we want to set these attributes
	// directly, and not assume the existing ProfileBundles.
	err := f.Client.Create(context.TODO(), testScan, nil)
	if err != nil {
		t.Fatalf("failed to create scan %s: %s", scanName, err)
	}
	defer f.Client.Delete(context.TODO(), testScan)

	// Wait for the scan to reach Done phase
	err = f.WaitForScanStatus(f.OperatorNamespace, scanName, compv1alpha1.PhaseDone)
	if err != nil {
		t.Fatal(err)
	}

	// Get the final scan state
	if err = f.Client.Get(context.TODO(), types.NamespacedName{Name: scanName, Namespace: f.OperatorNamespace}, testScan); err != nil {
		t.Fatal(err)
	}

	// The scan should NOT fail on profile deprecation check when ProfileBundle matching fails
	if testScan.Status.ErrorMessage == "Could not check whether the Profile used by ComplianceScan is deprecated" {
		t.Fatal(errors.New("scan should not fail on profile deprecation check when ProfileBundle matching fails"))
	}

	t.Logf("Scan completed with result: %s", testScan.Status.Result)
}

// TestRuleVariableAnnotation tests that rules with variables have the correct annotation
func TestRuleVariableAnnotation(t *testing.T) {
	t.Parallel()
	f := framework.Global

	// Test cases for rules that should have variable annotations
	testCases := []struct {
		ruleName         string
		expectedVariable string
		description      string
	}{
		{
			ruleName:         "ocp4-configure-network-policies-namespaces",
			expectedVariable: "var-network-policies-namespaces-exempt-regex",
			description:      "Network policies namespace exemption variable",
		},
		{
			ruleName:         "ocp4-resource-requests-limits-in-statefulset",
			expectedVariable: "var-statefulset-limit-namespaces-exempt-regex",
			description:      "StatefulSet resource limit namespace exemption variable",
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.ruleName, func(t *testing.T) {
			// Get the rule
			rule := &compv1alpha1.Rule{}
			err := f.Client.Get(context.TODO(), types.NamespacedName{
				Name:      tc.ruleName,
				Namespace: f.OperatorNamespace,
			}, rule)
			if err != nil {
				t.Fatalf("Failed to get rule %s: %v", tc.ruleName, err)
			}

			// Check that the rule has the variable annotation
			variableAnnotation, exists := rule.Annotations[compv1alpha1.RuleVariableAnnotationKey]
			if !exists {
				t.Fatalf("Rule %s is missing the %s annotation. This is a regression of CMP-3582",
					tc.ruleName, compv1alpha1.RuleVariableAnnotationKey)
			}

			// Verify the annotation contains the expected variable
			if variableAnnotation != tc.expectedVariable {
				t.Fatalf("Rule %s has incorrect variable annotation.\nExpected: %s\nGot: %s\nDescription: %s",
					tc.ruleName, tc.expectedVariable, variableAnnotation, tc.description)
			}

			t.Logf("Rule %s correctly has variable annotation: %s", tc.ruleName, tc.expectedVariable)
		})
	}
}
