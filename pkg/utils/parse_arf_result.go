package utils

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/subchen/go-xmldom"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	machineConfigFixType = "urn:xccdf:fix:script:ignition"
	rulePrefix           = "xccdf_org.ssgproject.content_rule_"
)

// XMLDocument is a wrapper that keeps the interface XML-parser-agnostic
type XMLDocument struct {
	*xmldom.Document
}

type ParseResult struct {
	Check       *compv1alpha1.ComplianceCheck
	Remediation *compv1alpha1.ComplianceRemediation
}

type ruleHashTable map[string]*xmldom.Node

func newRuleHashTable(dsDom *XMLDocument) ruleHashTable {
	benchmarkDom := dsDom.Root.QueryOne("//component/Benchmark")
	rules := benchmarkDom.Query("//Rule")

	table := make(ruleHashTable)
	for i := range rules {
		ruleDefinition := rules[i]
		ruleId := ruleDefinition.GetAttributeValue("id")

		table[ruleId] = ruleDefinition
	}

	return table
}

// ParseContent parses the DataStream and returns the XML document
func ParseContent(dsReader io.Reader) (*XMLDocument, error) {
	dsDom, err := xmldom.Parse(dsReader)
	if err != nil {
		return nil, err
	}
	return &XMLDocument{dsDom}, nil
}

func ParseResultsFromContentAndXccdf(scheme *runtime.Scheme, scanName string, namespace string,
	dsDom *XMLDocument, resultsReader io.Reader) ([]*ParseResult, error) {

	resultsDom, err := xmldom.Parse(resultsReader)
	if err != nil {
		return nil, err
	}

	ruleTable := newRuleHashTable(dsDom)

	results := resultsDom.Root.Query("//rule-result")
	parsedResults := make([]*ParseResult, 0)
	for i := range results {
		result := results[i]
		ruleIDRef := result.GetAttributeValue("idref")
		if ruleIDRef == "" {
			continue
		}

		resultRule := ruleTable[ruleIDRef]
		if resultRule == nil {
			continue
		}

		resCheck, err := newComplianceCheck(result, resultRule, ruleIDRef, scanName, namespace)
		if err != nil {
			continue
		}

		if resCheck != nil {
			pr := &ParseResult{
				Check: resCheck,
			}

			if resCheck.Spec.Result == compv1alpha1.CheckResultFail || resCheck.Spec.Result == compv1alpha1.CheckResultInfo {
				pr.Remediation = newComplianceRemediation(scheme, scanName, namespace, resultRule)
			}

			parsedResults = append(parsedResults, pr)
		}
	}

	return parsedResults, nil
}

// Returns a new complianceCheck if the check data is usable
func newComplianceCheck(result *xmldom.Node, rule *xmldom.Node, ruleIdRef, scanName, namespace string) (*compv1alpha1.ComplianceCheck, error) {
	name := nameFromId(scanName, ruleIdRef)
	mappedResult, err := mapComplianceCheckResult(result)
	if err != nil {
		return nil, err
	}
	if mappedResult == compv1alpha1.CheckResultNoResult {
		return nil, nil
	}

	return &compv1alpha1.ComplianceCheck{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: compv1alpha1.ComplianceCheckSpec{
			ID:          ruleIdRef,
			Result:      mappedResult,
			Description: complianceCheckDescription(rule),
		},
	}, nil
}

func getSafeText(nptr *xmldom.Node, elem string) string {
	elemNode := nptr.FindOneByName(elem)
	if elemNode == nil {
		return ""
	}

	return elemNode.Text
}

func complianceCheckDescription(rule *xmldom.Node) string {
	title := getSafeText(rule, "title")
	if title != "" {
		title = title + "\n"
	}
	return title + getSafeText(rule, "rationale")
}

func mapComplianceCheckResult(result *xmldom.Node) (compv1alpha1.ComplianceCheckResult, error) {
	resultEl := result.FindOneByName("result")
	if resultEl == nil {
		return "", errors.New("result node has no 'result' attribute")
	}

	// All states can be found at https://csrc.nist.gov/CSRC/media/Publications/nistir/7275/rev-4/final/documents/nistir-7275r4_updated-march-2012_clean.pdf
	// section 6.6.4.2, table 26
	switch resultEl.Text {
	// The standard says that "Fixed means the rule failed initially but was then fixed"
	case "pass", "fixed":
		return compv1alpha1.CheckResultPass, nil
	case "fail":
		return compv1alpha1.CheckResultFail, nil
		// Unknown state is when the rule runs to completion, but then the results can't be interpreted
	case "error", "unknown":
		return compv1alpha1.CheckResultError, nil
		// We map both notchecked and info to Info. Notchecked means the rule does not even have a check,
		// and the administratos must inspect the rule manually (e.g. disable something in BIOS),
		// informational means that the rule has a check which failed, but the severity is low, depending
		// on the environment (e.g. disable USB support completely from the kernel cmdline)
	case "notchecked", "informational":
		return compv1alpha1.CheckResultInfo, nil
		// We map notapplicable to Skipped. Notapplicable means the rule was selected
		// but does not apply to the current configuration (e.g. arch-specific),
	case "notapplicable":
		return compv1alpha1.CheckResultSkipped, nil
	case "notselected":
		// We map notselected to nothing, as the test wasn't included in the benchmark
		return compv1alpha1.CheckResultNoResult, nil
	}

	return compv1alpha1.CheckResultNoResult, fmt.Errorf("couldn't match %s to a known state", resultEl.Text)
}

func newComplianceRemediation(scheme *runtime.Scheme, scanName, namespace string, rule *xmldom.Node) *compv1alpha1.ComplianceRemediation {
	for _, fix := range rule.FindByName("fix") {
		if !isMachineConfigFix(fix) {
			continue
		}

		return remediationFromFixElement(scheme, fix, scanName, namespace)
	}

	return nil
}

func isMachineConfigFix(fix *xmldom.Node) bool {
	if fix.GetAttributeValue("system") == machineConfigFixType {
		return true
	}
	return false
}

func nameFromId(scanName, ruleIdRef string) string {
	ruleName := strings.TrimPrefix(ruleIdRef, rulePrefix)
	dnsFriendlyFixId := strings.ReplaceAll(ruleName, "_", "-")
	dnsFriendlyFixId = strings.ToLower(dnsFriendlyFixId)
	return fmt.Sprintf("%s-%s", scanName, dnsFriendlyFixId)
}

func remediationFromFixElement(scheme *runtime.Scheme, fix *xmldom.Node, scanName, namespace string) *compv1alpha1.ComplianceRemediation {
	fixId := fix.GetAttributeValue("id")
	if fixId == "" {
		return nil
	}

	dnsFriendlyFixId := strings.ReplaceAll(fixId, "_", "-")
	remName := fmt.Sprintf("%s-%s", scanName, dnsFriendlyFixId)
	return remediationFromString(scheme, remName, namespace, fix.Text)
}

func remediationFromString(scheme *runtime.Scheme, name string, namespace string, mcContent string) *compv1alpha1.ComplianceRemediation {
	mcObject, err := rawObjectToMachineConfig(scheme, []byte(mcContent))
	if err != nil {
		return nil
	}

	return &compv1alpha1.ComplianceRemediation{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Type:  compv1alpha1.McRemediation,
				Apply: false,
			},
			MachineConfigContents: *mcObject,
		},
		Status: compv1alpha1.ComplianceRemediationStatus{
			ApplicationState: compv1alpha1.RemediationNotApplied,
		},
	}
}

func rawObjectToMachineConfig(scheme *runtime.Scheme, in []byte) (*mcfgv1.MachineConfig, error) {
	mcfgCodecs := serializer.NewCodecFactory(scheme)
	m, err := runtime.Decode(mcfgCodecs.UniversalDecoder(mcfgv1.SchemeGroupVersion), in)

	if err != nil {
		return nil, fmt.Errorf("failed to decode raw bytes to mcfgv1.SchemeGroupVersion: %v", err)
	}
	if m == nil {
		return nil, fmt.Errorf("expected mcfgv1.SchemeGroupVersion but got nil")
	}

	mc, ok := m.(*mcfgv1.MachineConfig)
	if !ok {
		return nil, fmt.Errorf("expected *mcfvgv1.MachineConfig but found %T", m)
	}

	// FIXME: Should we check the MC is valid? That at least one of the .spec fields
	// are present?

	// This might be a bug in the schema perhaps? If there are no kargs, the list is nill,
	// but the MCO doesn't like that. Let's make sure the list is empty
	if mc.Spec.KernelArguments == nil {
		mc.Spec.KernelArguments = make([]string, 0)
	}
	return mc, nil
}
