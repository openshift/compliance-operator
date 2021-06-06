package utils

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/antchfx/xmlquery"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

const (
	machineConfigFixType    = "urn:xccdf:fix:script:ignition"
	kubernetesFixType       = "urn:xccdf:fix:script:kubernetes"
	ocilCheckType           = "http://scap.nist.gov/schema/ocil/2"
	rulePrefix              = "xccdf_org.ssgproject.content_rule_"
	questionnaireSuffix     = "_ocil:questionnaire:1"
	questionSuffix          = "_question:question:1"
	dependencyAnnotationKey = "complianceascode.io/depends-on"
)

// Constants useful for parsing warnings
const (
	endPointTag = "ocp-api-endpoint"
)

type ParseResult struct {
	Id          string
	CheckResult *compv1alpha1.ComplianceCheckResult
	Remediation *compv1alpha1.ComplianceRemediation
}

// getPathsFromRuleWarning finds the API endpoint from in. The expected structure is:
//
//  <warning category="general" lang="en-US"><code class="ocp-api-endpoint">/apis/config.openshift.io/v1/oauths/cluster
//  </code></warning>
func GetPathFromWarningXML(in *xmlquery.Node) string {
	codeNodes := in.SelectElements("html:code")

	for _, codeNode := range codeNodes {
		if codeNode.SelectAttr("class") == endPointTag {
			return codeNode.InnerText()
		}
	}

	return ""
}

type nodeByIdHashTable map[string]*xmlquery.Node

func newByIdHashTable(nodes []*xmlquery.Node) nodeByIdHashTable {
	table := make(nodeByIdHashTable)
	for i := range nodes {
		ruleDefinition := nodes[i]
		ruleId := ruleDefinition.SelectAttr("id")

		table[ruleId] = ruleDefinition
	}

	return table
}

func newHashTableFromRootAndQuery(dsDom *xmlquery.Node, root, query string) nodeByIdHashTable {
	benchmarkDom := dsDom.SelectElement(root)
	rules := benchmarkDom.SelectElements(query)
	return newByIdHashTable(rules)
}

func newRuleHashTable(dsDom *xmlquery.Node) nodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//ds:component/xccdf-1.2:Benchmark", "//xccdf-1.2:Rule")
}

func newOcilQuestionTable(dsDom *xmlquery.Node) nodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//ds:component/ocil:ocil", "//ocil:boolean_question")
}

func getRuleOcilQuestionID(rule *xmlquery.Node) string {
	var ocilRefEl *xmlquery.Node

	for _, check := range rule.SelectElements("//xccdf-1.2:check") {
		if check.SelectAttr("system") == ocilCheckType {
			ocilRefEl = check.SelectElement("xccdf-1.2:check-content-ref")
			break
		}
	}

	if ocilRefEl == nil {
		return ""
	}

	questionnareName := ocilRefEl.SelectAttr("name")
	if strings.HasSuffix(questionnareName, questionnaireSuffix) == false {
		return ""
	}

	return strings.TrimSuffix(questionnareName, questionnaireSuffix) + questionSuffix
}

func getInstructionsForRule(rule *xmlquery.Node, ocilTable nodeByIdHashTable) string {
	// convert rule's questionnaire ID to question ID
	ruleQuestionId := getRuleOcilQuestionID(rule)

	// look up the node
	questionNode, ok := ocilTable[ruleQuestionId]
	if !ok {
		return ""
	}

	// if not found, return empty string
	textNode := questionNode.SelectElement("ocil:question_text")
	if textNode == nil {
		return ""
	}

	// if found, strip the last line
	textSlice := strings.Split(strings.TrimSpace(textNode.InnerText()), "\n")
	if len(textSlice) > 1 {
		textSlice = textSlice[:len(textSlice)-1]
	}

	return strings.TrimSpace(strings.Join(textSlice, "\n"))
}

// ParseContent parses the DataStream and returns the XML document
func ParseContent(dsReader io.Reader) (*xmlquery.Node, error) {
	dsDom, err := xmlquery.Parse(dsReader)
	if err != nil {
		return nil, err
	}
	return dsDom, nil
}

func ParseResultsFromContentAndXccdf(scheme *runtime.Scheme, scanName string, namespace string,
	dsDom *xmlquery.Node, resultsReader io.Reader) ([]*ParseResult, error) {

	resultsDom, err := xmlquery.Parse(resultsReader)
	if err != nil {
		return nil, err
	}

	ruleTable := newRuleHashTable(dsDom)
	questionsTable := newOcilQuestionTable(dsDom)

	results := resultsDom.SelectElements("//rule-result")
	parsedResults := make([]*ParseResult, 0)
	for i := range results {
		result := results[i]
		ruleIDRef := result.SelectAttr("idref")
		if ruleIDRef == "" {
			continue
		}

		resultRule := ruleTable[ruleIDRef]
		if resultRule == nil {
			continue
		}

		instructions := getInstructionsForRule(resultRule, questionsTable)
		resCheck, err := newComplianceCheckResult(result, resultRule, ruleIDRef, instructions, scanName, namespace)
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
func newComplianceCheckResult(result *xmlquery.Node, rule *xmlquery.Node, ruleIdRef, instructions, scanName, namespace string) (*compv1alpha1.ComplianceCheckResult, error) {
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
		ID:           ruleIdRef,
		Status:       mappedStatus,
		Severity:     mappedSeverity,
		Instructions: instructions,
		Description:  complianceCheckResultDescription(rule),
		Warnings:     getWarningsForRule(rule),
	}, nil
}

func getSafeText(nptr *xmlquery.Node, elem string) string {
	elemNode := nptr.SelectElement(elem)
	if elemNode == nil {
		return ""
	}

	return elemNode.InnerText()
}

func complianceCheckResultDescription(rule *xmlquery.Node) string {
	title := getSafeText(rule, "xccdf-1.2:title")
	if title != "" {
		title = title + "\n"
	}
	return title + getSafeText(rule, "xccdf-1.2:rationale")
}

func getWarningsForRule(rule *xmlquery.Node) []string {
	warningObjs := rule.SelectElements("//xccdf-1.2:warning")

	warnings := []string{}

	for _, warn := range warningObjs {
		if warn == nil {
			continue
		}
		// We skip this warning if it's relevant
		// to parsing the API paths.
		if GetPathFromWarningXML(warn) != "" {
			continue
		}
		warnings = append(warnings, XmlNodeAsMarkdown(warn))
	}

	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func mapComplianceCheckResultSeverity(result *xmlquery.Node) (compv1alpha1.ComplianceCheckResultSeverity, error) {
	severityAttr := result.SelectAttr("severity")
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

func mapComplianceCheckResultStatus(result *xmlquery.Node) (compv1alpha1.ComplianceCheckStatus, error) {
	resultEl := result.SelectElement("result")
	if resultEl == nil {
		return "", errors.New("result node has no 'result' attribute")
	}

	// All states can be found at https://csrc.nist.gov/CSRC/media/Publications/nistir/7275/rev-4/final/documents/nistir-7275r4_updated-march-2012_clean.pdf
	// section 6.6.4.2, table 26
	switch resultEl.InnerText() {
	// The standard says that "Fixed means the rule failed initially but was then fixed"
	case "pass", "fixed":
		return compv1alpha1.CheckResultPass, nil
	case "fail":
		return compv1alpha1.CheckResultFail, nil
		// Unknown state is when the rule runs to completion, but then the results can't be interpreted
	case "error", "unknown":
		return compv1alpha1.CheckResultError, nil
		// Notchecked means the rule does not even have a check,
		// and the administrators must inspect the rule manually (e.g. disable something in BIOS),
	case "notchecked":
		return compv1alpha1.CheckResultManual, nil
		// informational means that the rule has a check which failed, but the severity is low, depending
		// on the environment (e.g. disable USB support completely from the kernel cmdline)
	case "informational":
		return compv1alpha1.CheckResultInfo, nil
		// We map notapplicable to Skipped. Notapplicable means the rule was selected
		// but does not apply to the current configuration (e.g. arch-specific),
	case "notapplicable":
		return compv1alpha1.CheckResultNotApplicable, nil
	case "notselected":
		// We map notselected to nothing, as the test wasn't included in the benchmark
		return compv1alpha1.CheckResultNoResult, nil
	}

	return compv1alpha1.CheckResultNoResult, fmt.Errorf("couldn't match %s to a known state", resultEl.InnerText())
}

func newComplianceRemediation(scheme *runtime.Scheme, scanName, namespace string, rule *xmlquery.Node) *compv1alpha1.ComplianceRemediation {
	for _, fix := range rule.SelectElements("//xccdf-1.2:fix") {
		if isRelevantFix(fix) {
			return remediationFromFixElement(scheme, fix, scanName, namespace)
		}
	}

	return nil
}

func isRelevantFix(fix *xmlquery.Node) bool {
	if fix.SelectAttr("system") == machineConfigFixType {
		return true
	}
	if fix.SelectAttr("system") == kubernetesFixType {
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

func remediationFromFixElement(scheme *runtime.Scheme, fix *xmlquery.Node, scanName, namespace string) *compv1alpha1.ComplianceRemediation {
	fixId := fix.SelectAttr("id")
	if fixId == "" {
		return nil
	}

	dnsFriendlyFixId := strings.ReplaceAll(fixId, "_", "-")
	remName := fmt.Sprintf("%s-%s", scanName, dnsFriendlyFixId)
	// TODO(OZZ) fix text
	return remediationFromString(scheme, remName, namespace, fix.InnerText())
}

func remediationFromString(scheme *runtime.Scheme, name string, namespace string, fixContent string) *compv1alpha1.ComplianceRemediation {
	obj, err := rawObjectToUnstructured(scheme, fixContent)
	if err != nil {
		return nil
	}

	annotations := make(map[string]string)

	if hasDependencyAnnotation(obj) {
		annotations = handleDependencyAnnotation(obj)
	}

	return &compv1alpha1.ComplianceRemediation{
		ObjectMeta: v1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: false,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: obj,
			},
		},
		Status: compv1alpha1.ComplianceRemediationStatus{
			ApplicationState: compv1alpha1.RemediationPending,
		},
	}
}

func hasDependencyAnnotation(u *unstructured.Unstructured) bool {
	annotations := u.GetAnnotations()
	if annotations == nil {
		return false
	}

	_, hasAnnotation := annotations[dependencyAnnotationKey]
	return hasAnnotation
}

func handleDependencyAnnotation(u *unstructured.Unstructured) map[string]string {
	outputAnnotations := make(map[string]string)

	// We already assume this has some annotation
	inAnns := u.GetAnnotations()

	// parse
	dependencies := inAnns[dependencyAnnotationKey]

	// set dependencies
	outputAnnotations[compv1alpha1.RemediationDependencyAnnotation] = dependencies

	// reset metadata of output object
	delete(inAnns, dependencyAnnotationKey)
	u.SetAnnotations(inAnns)

	return outputAnnotations
}

func rawObjectToUnstructured(scheme *runtime.Scheme, in string) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	dec := k8syaml.NewYAMLToJSONDecoder(strings.NewReader(in))
	err := dec.Decode(obj)
	return obj, err
}
