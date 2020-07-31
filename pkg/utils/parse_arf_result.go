package utils

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/subchen/go-xmldom"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

const (
	machineConfigFixType = "urn:xccdf:fix:script:ignition"
	kubernetesFixType    = "urn:xccdf:fix:script:kubernetes"
	rulePrefix           = "xccdf_org.ssgproject.content_rule_"
)

// XMLDocument is a wrapper that keeps the interface XML-parser-agnostic
type XMLDocument struct {
	*xmldom.Document
}

type ParseResult struct {
	Id          string
	CheckResult *compv1alpha1.ComplianceCheckResult
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

		resCheck, err := newComplianceCheckResult(result, resultRule, ruleIDRef, scanName, namespace)
		if err != nil {
			continue
		}

		if resCheck != nil {
			pr := &ParseResult{
				Id:          ruleIDRef,
				CheckResult: resCheck,
			}

			pr.Remediation = newComplianceRemediation(scheme, scanName, namespace, resultRule)
			parsedResults = append(parsedResults, pr)
		}
	}

	return parsedResults, nil
}

// Returns a new complianceCheckResult if the check data is usable
func newComplianceCheckResult(result *xmldom.Node, rule *xmldom.Node, ruleIdRef, scanName, namespace string) (*compv1alpha1.ComplianceCheckResult, error) {
	name := nameFromId(scanName, ruleIdRef)
	mappedStatus, err := mapComplianceCheckResultStatus(result)
	if err != nil {
		return nil, err
	}
	if mappedStatus == compv1alpha1.CheckResultNoResult {
		return nil, nil
	}

	mappedSeverity, err := mapComplianceCheckResultSeverity(rule)
	if err != nil {
		return nil, err
	}

	return &compv1alpha1.ComplianceCheckResult{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		ID:          ruleIdRef,
		Status:      mappedStatus,
		Severity:    mappedSeverity,
		Description: complianceCheckResultDescription(rule),
	}, nil
}

func getSafeText(nptr *xmldom.Node, elem string) string {
	elemNode := nptr.FindOneByName(elem)
	if elemNode == nil {
		return ""
	}

	return elemNode.Text
}

func complianceCheckResultDescription(rule *xmldom.Node) string {
	title := getSafeText(rule, "title")
	if title != "" {
		title = title + "\n"
	}
	return title + getSafeText(rule, "rationale")
}

func mapComplianceCheckResultSeverity(result *xmldom.Node) (compv1alpha1.ComplianceCheckResultSeverity, error) {
	severityAttr := result.GetAttributeValue("severity")
	if severityAttr == "" {
		return "", errors.New("result node has no 'severity' attribute")
	}

	// All severities can be found in https://csrc.nist.gov/CSRC/media/Publications/nistir/7275/rev-4/final/documents/nistir-7275r4_updated-march-2012_clean.pdf
	// section 6.6.4.2 table 9
	switch severityAttr {
	case "unknown":
		return compv1alpha1.CheckResultSeverityUnknown, nil
	case "info":
		return compv1alpha1.CheckResultSeverityInfo, nil
	case "low":
		return compv1alpha1.CheckResultSeverityLow, nil
	case "medium":
		return compv1alpha1.CheckResultSeverityMedium, nil
	case "high":
		return compv1alpha1.CheckResultSeverityHigh, nil
	}

	return compv1alpha1.CheckResultSeverityUnknown, nil
}

func mapComplianceCheckResultStatus(result *xmldom.Node) (compv1alpha1.ComplianceCheckStatus, error) {
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
		if isRelevantFix(fix) {
			return remediationFromFixElement(scheme, fix, scanName, namespace)
		}
	}

	return nil
}

func isRelevantFix(fix *xmldom.Node) bool {
	if fix.GetAttributeValue("system") == machineConfigFixType {
		return true
	}
	if fix.GetAttributeValue("system") == kubernetesFixType {
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

func remediationFromString(scheme *runtime.Scheme, name string, namespace string, fixContent string) *compv1alpha1.ComplianceRemediation {
	obj, err := rawObjectToUnstructured(scheme, fixContent)
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
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: obj,
			},
		},
		Status: compv1alpha1.ComplianceRemediationStatus{
			ApplicationState: compv1alpha1.RemediationNotApplied,
		},
	}
}

func rawObjectToUnstructured(scheme *runtime.Scheme, in string) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	dec := k8syaml.NewYAMLToJSONDecoder(strings.NewReader(in))
	err := dec.Decode(obj)
	return obj, err
}
