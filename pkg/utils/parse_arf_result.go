package utils

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/go-logr/logr"
	"github.com/subchen/go-xmldom"
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
	endPointTag    = "ocp-api-endpoint"
	endPointTagEnd = endPointTag + "\">"
	codeTag        = "</code>"
)

// check types
const (
	// Checks whether an endpoint is compliant with a specific policy.
	ovalCheckTypeCompliance = "compliance"
	// Checks whether specific software is installed on the endpoint.
	ovalCheckTypeInventory = "inventory"
	// OVAL Definitions that do not fall into one of the other defined classes.
	ovalCheckTypeMiscellaneous = "miscellaneous"
	// Checks whether a patch needs to be installed on an endpoint.
	ovalCheckTypePatch = "patch"
	// Checks whether an endpoint is vulnerable.
	ovalCheckTypeVulnerability = "vulnerability"
)

var (
	ErrEmptyStatus = errors.New("rule contained an empty result status")
)

// XMLDocument is a wrapper that keeps the interface XML-parser-agnostic
type XMLDocument struct {
	*xmldom.Document
}

type ParseResult struct {
	CheckResult *compv1alpha1.ComplianceCheckResult
	Remediation *compv1alpha1.ComplianceRemediation
}

// getPathsFromRuleWarning finds the API endpoint from in. The expected structure is:
//
//  <warning category="general" lang="en-US"><code class="ocp-api-endpoint">/apis/config.openshift.io/v1/oauths/cluster
//  </code></warning>
func GetPathFromWarningXML(in string) string {
	apiIndex := strings.Index(in, endPointTag)
	if apiIndex == -1 {
		return ""
	}

	apiValueBeginIndex := apiIndex + len(endPointTagEnd)
	apiValueEndIndex := strings.Index(in[apiValueBeginIndex:], codeTag)
	if apiValueEndIndex == -1 {
		return ""
	}

	return in[apiValueBeginIndex : apiValueBeginIndex+apiValueEndIndex]
}

type nodeByIdHashTable map[string]*xmldom.Node

func newByIdHashTable(nodes []*xmldom.Node) nodeByIdHashTable {
	table := make(nodeByIdHashTable)
	for i := range nodes {
		ruleDefinition := nodes[i]
		ruleId := ruleDefinition.GetAttributeValue("id")

		table[ruleId] = ruleDefinition
	}

	return table
}

func newHashTableFromRootAndQuery(dsDom *XMLDocument, root, query string) nodeByIdHashTable {
	benchmarkDom := dsDom.Root.QueryOne(root)
	rules := benchmarkDom.Query(query)
	return newByIdHashTable(rules)
}

func newRuleHashTable(dsDom *XMLDocument) nodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//component/Benchmark", "//Rule")
}

func newOcilQuestionTable(dsDom *XMLDocument) nodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//component/ocil", "//boolean_question")
}

func getRuleOcilQuestionID(rule *xmldom.Node) string {
	var ocilRefEl *xmldom.Node

	for _, check := range rule.FindByName("check") {
		if check.GetAttributeValue("system") == ocilCheckType {
			ocilRefEl = check.FindOneByName("check-content-ref")
			break
		}
	}

	if ocilRefEl == nil {
		return ""
	}

	questionnareName := ocilRefEl.GetAttributeValue("name")
	if strings.HasSuffix(questionnareName, questionnaireSuffix) == false {
		return ""
	}

	return strings.TrimSuffix(questionnareName, questionnaireSuffix) + questionSuffix
}

func getInstructionsForRule(rule *xmldom.Node, ocilTable nodeByIdHashTable) string {
	// convert rule's questionnaire ID to question ID
	ruleQuestionId := getRuleOcilQuestionID(rule)

	// look up the node
	questionNode, ok := ocilTable[ruleQuestionId]
	if !ok {
		return ""
	}

	// if not found, return empty string
	textNode := questionNode.FindOneByName("question_text")
	if textNode == nil {
		return ""
	}

	// if found, strip the last line
	textSlice := strings.Split(textNode.Text, "\n")
	if len(textSlice) > 1 {
		textSlice = textSlice[:len(textSlice)-1]
	}

	return strings.TrimSpace(strings.Join(textSlice, "\n"))
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
	dsDom *XMLDocument, resultsReader io.Reader, l logr.Logger) (map[string]*ParseResult, error) {
	resultsDom, err := xmldom.Parse(resultsReader)
	if err != nil {
		return nil, err
	}

	ruleTable := newRuleHashTable(dsDom)
	questionsTable := newOcilQuestionTable(dsDom)

	results := resultsDom.Root.Query("//rule-result")
	parsedResults := make(map[string]*ParseResult)
	for i := range results {
		result := results[i]
		ruleIDRef := result.GetAttributeValue("idref")
		if ruleIDRef == "" {
			l.Info("Skipping rule without 'idref'")
			continue
		}

		rlog := l.WithValues("rule-id", ruleIDRef)

		resultRule := ruleTable[ruleIDRef]
		if resultRule == nil {
			rlog.Info("Skipping rule not found in data-stream")
			continue
		}

		// This belongs to an already parsed result... Merge
		if storedResult, ok := parsedResults[ruleIDRef]; ok {
			if !ruleIsMultiCheck(resultRule) {
				rlog.Info("Skipping rule result since it's already been parsed and it's not multi-check")
				continue
			}

			err := mergeCheckResult(result, storedResult.CheckResult)
			if err != nil {
				rlog.Error(err, "Skipping rule due to merging error")
			}

			setFailureInfoIfNeeded(storedResult.CheckResult, result, resultRule, dsDom.Root, rlog)
			continue
		}

		instructions := getInstructionsForRule(resultRule, questionsTable)
		resCheck, err := newComplianceCheckResult(result, resultRule, ruleIDRef, instructions, scanName, namespace)
		if errors.Is(err, ErrEmptyStatus) {
			// skip without logging. This is fine.
			continue
		}
		if err != nil {
			rlog.Error(err, "Skipping rule due to parsing error")
			continue
		}

		pr := &ParseResult{
			CheckResult: resCheck,
		}

		setFailureInfoIfNeeded(resCheck, result, resultRule, dsDom.Root, rlog)

		// We only need to parse the remediation once
		pr.Remediation = newComplianceRemediation(scheme, scanName, namespace, resultRule)

		// Found a new result, persist it in the hash
		parsedResults[ruleIDRef] = pr
	}

	return parsedResults, nil
}

// Returns a new complianceCheckResult if the check data is usable
func newComplianceCheckResult(result *xmldom.Node, rule *xmldom.Node, ruleIdRef, instructions, scanName, namespace string) (*compv1alpha1.ComplianceCheckResult, error) {
	name := nameFromId(scanName, ruleIdRef)
	mappedStatus, err := mapComplianceCheckResultStatus(result)
	if err != nil {
		return nil, err
	}
	if mappedStatus == compv1alpha1.CheckResultNoResult {
		return nil, ErrEmptyStatus
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

// Merges a new found result with a pre-existing one
func mergeCheckResult(newResult *xmldom.Node, storedResult *compv1alpha1.ComplianceCheckResult) error {
	mappedStatus, err := mapComplianceCheckResultStatus(newResult)
	if err != nil {
		return err
	}

	// TODO(jaosorior): Mark a failure somehow

	storedResult.Status = compv1alpha1.CompareCheckResults(mappedStatus, storedResult.Status)
	return nil
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

func getWarningsForRule(rule *xmldom.Node) []string {
	warningObjs := rule.FindByName("warning")

	warnings := []string{}

	for _, warn := range warningObjs {
		if warn == nil {
			continue
		}
		// We skip this warning if it's relevant
		// to parsing the API paths.
		if GetPathFromWarningXML(warn.XML()) != "" {
			continue
		}
		warnings = append(warnings, warn.Text)
	}

	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func setFailureInfoIfNeeded(resCheck *compv1alpha1.ComplianceCheckResult, result, resultRule, root *xmldom.Node, l logr.Logger) {
	// HACK(jaosorior): this only adds evidence for multi-check rules...
	// This is because the only multi-check rule we're currently handling is
	// security_patches_up_to_date, for which we can gather evidence.
	// If we want to support gathering evidence for other checks, we can remove the
	// multi-check verification below.
	// This is skipped to save compute time.
	if resCheck.Status == compv1alpha1.CheckResultFail && ruleIsMultiCheck(resultRule) {
		def, defErr := getCheckDefinition(result, root)
		if defErr != nil {
			l.Error(defErr, "Couldn't get OVAL definition.")
		} else {
			// Currently, we only support evidence for "patch" checks
			if strings.EqualFold(def.GetAttributeValue("class"), ovalCheckTypePatch) {
				getFailureRefsForPatchClass(resCheck, def)
			}
		}
	}
}

func getCheckDefinition(result *xmldom.Node, root *xmldom.Node) (*xmldom.Node, error) {
	ccref := result.FindOneByName("check-content-ref")
	if ccref == nil {
		return nil, errors.New("didn't find 'check-content-ref' for result")
	}
	ccrefName := ccref.GetAttributeValue("name")
	if ccrefName == "" {
		return nil, errors.New("didn't find 'name' attribute in 'check-content-ref'")
	}

	reports := root.FindByName("report")
	for _, report := range reports {
		// Skip non-oval reports
		if !strings.HasPrefix(report.GetAttributeValue("id"), "oval") {
			continue
		}

		def := report.FindByID(ccrefName)
		if !strings.Contains(def.Name, "definition") {
			// There is something wrong with the OVAL and
			// we can't get the appropriate definition
			return nil, fmt.Errorf("OVAL check with ID '%s' isn't appropriate 'definition' object", ccrefName)
		}
		return def, nil
	}

	return nil, fmt.Errorf("OVAL check with ID '%s' not found", ccrefName)
}

func getFailureRefsForPatchClass(rescheck *compv1alpha1.ComplianceCheckResult, def *xmldom.Node) {
	title := def.FindOneByName("title")
	if title == nil {
		return
	}

	output := title.Text
	outrefs := make([]string, 0)
	refs := def.FindByName("reference")

	for _, ref := range refs {
		refid := ref.GetAttributeValue("ref_id")
		if refid != "" {
			outrefs = append(outrefs, refid)
		}
	}

	if len(outrefs) > 0 {
		output += "\nReferences: " + strings.Join(outrefs, ",")
	}

	if rescheck.FailureInfo == nil {
		rescheck.FailureInfo = make([]string, 0)
	}
	rescheck.FailureInfo = append(rescheck.FailureInfo, output)
}

// ruleIsMultiCheck: multi-check rules can output several results;
// this allows us to handle that.
func ruleIsMultiCheck(rule *xmldom.Node) bool {
	check := rule.FindOneByName("check")
	if check == nil {
		return false
	}
	return check.GetAttributeValue("multi-check") == "true"
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
