package utils

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"text/template/parse"

	"github.com/antchfx/xmlquery"
	"github.com/pkg/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

const (
	machineConfigFixType            = "urn:xccdf:fix:script:ignition"
	kubernetesFixType               = "urn:xccdf:fix:script:kubernetes"
	ocilCheckType                   = "http://scap.nist.gov/schema/ocil/2"
	rulePrefix                      = "xccdf_org.ssgproject.content_rule_"
	valuePrefix                     = "xccdf_org.ssgproject.content_value_"
	ruleValueSuffix                 = ":var:1"
	questionnaireSuffix             = "_ocil:questionnaire:1"
	questionSuffix                  = "_question:question:1"
	ovalCheckPrefix                 = "oval:ssg-"
	objValuePrefix                  = "oval:ssg-variable"
	ovalCheckType                   = "http://oval.mitre.org/XMLSchema/oval-definitions-5"
	dependencyAnnotationKey         = "complianceascode.io/depends-on"
	kubeDependencyAnnotationKey     = "complianceascode.io/depends-on-obj"
	remediationTypeAnnotationKey    = "complianceascode.io/remediation-type"
	enforcementTypeAnnotationKey    = "complianceascode.io/enforcement-type"
	optionalAnnotationKey           = "complianceascode.io/optional"
	valueInputRequiredAnnotationKey = "complianceascode.io/value-input-required"
	//index to trim `{{`and`}}`
	trimStartIndex = 2
	trimEndIndex   = 2
)

// Constants useful for parsing warnings
const (
	endPointTag           = "ocp-api-endpoint"
	dumpLocationClass     = "ocp-dump-location"
	filterTypeClass       = "ocp-api-filter"
	filteredEndpointClass = "filtered"
)

type ParseResult struct {
	Id           string
	CheckResult  *compv1alpha1.ComplianceCheckResult
	Remediations []*compv1alpha1.ComplianceRemediation
}

type ResourcePath struct {
	ObjPath  string
	DumpPath string
	Filter   string
}

// getPathsFromRuleWarning finds the API endpoint from in. The expected structure is:
//
//  <warning category="general" lang="en-US"><code class="ocp-api-endpoint">/apis/config.openshift.io/v1/oauths/cluster
//  </code></warning>
func GetPathFromWarningXML(in *xmlquery.Node) []ResourcePath {
	apiPaths := []ResourcePath{}

	codeNodes := in.SelectElements("//html:code")

	for _, codeNode := range codeNodes {
		if strings.Contains(codeNode.SelectAttr("class"), endPointTag) {
			path := codeNode.InnerText()
			if len(path) == 0 {
				continue
			}
			dumpPath := path
			var filter string
			pathID := codeNode.SelectAttr("id")
			if pathID != "" {
				filterNode := in.SelectElement(fmt.Sprintf(`//*[@id="filter-%s"]`, pathID))
				dumpNode := in.SelectElement(fmt.Sprintf(`//*[@id="dump-%s"]`, pathID))
				if filterNode != nil && dumpNode != nil {
					filter = filterNode.InnerText()
					dumpPath = dumpNode.InnerText()
				}
			}
			apiPaths = append(apiPaths, ResourcePath{ObjPath: path, DumpPath: dumpPath, Filter: filter})
		}
	}

	return apiPaths
}

func warningHasApiObjects(in *xmlquery.Node) bool {
	codeNodes := in.SelectElements("//html:code")

	for _, codeNode := range codeNodes {
		if codeNode.SelectAttr("class") == endPointTag {
			return true
		}
	}

	return false
}

type NodeByIdHashTable map[string]*xmlquery.Node
type nodeByIdHashVariablesTable map[string][]string

func newByIdHashTable(nodes []*xmlquery.Node) NodeByIdHashTable {
	table := make(NodeByIdHashTable)
	for i := range nodes {
		ruleDefinition := nodes[i]
		ruleId := ruleDefinition.SelectAttr("id")

		table[ruleId] = ruleDefinition
	}

	return table
}

func newHashTableFromRootAndQuery(dsDom *xmlquery.Node, root, query string) NodeByIdHashTable {
	benchmarkDom := dsDom.SelectElement(root)
	rules := benchmarkDom.SelectElements(query)
	return newByIdHashTable(rules)
}

func newRuleHashTable(dsDom *xmlquery.Node) NodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//ds:component/xccdf-1.2:Benchmark", "//xccdf-1.2:Rule")
}

func NewOcilQuestionTable(dsDom *xmlquery.Node) NodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//ds:component/ocil:ocil", "//ocil:boolean_question")
}
func newStateHashTable(dsDom *xmlquery.Node) NodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//ds:component/oval-def:oval_definitions/oval-def:states", "*")
}

func newObjHashTable(dsDom *xmlquery.Node) NodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//ds:component/oval-def:oval_definitions/oval-def:objects", "*")
}

func NewDefHashTable(dsDom *xmlquery.Node) NodeByIdHashTable {
	return newHashTableFromRootAndQuery(dsDom, "//ds:component/oval-def:oval_definitions/oval-def:definitions", "*")
}

func newValueListTable(dsDom *xmlquery.Node, statesTable, objectsTable NodeByIdHashTable) nodeByIdHashVariablesTable {
	root := "//ds:component/oval-def:oval_definitions/oval-def:tests"
	testsDom := dsDom.SelectElement(root).SelectElements("*")
	table := make(nodeByIdHashVariablesTable)

	for i := range testsDom {
		testDefinition := testsDom[i]
		testId := testDefinition.SelectAttr("id")
		var valueListState []string
		var valueListObject []string
		var valueList []string

		states := testDefinition.SelectElements("//ind:state")
		if len(states) > 0 {
			for i := range states {
				if states[i] == nil {
					continue
				}

				state, ok := statesTable[states[i].SelectAttr("state_ref")]
				if !ok {
					continue
				}
				valueListStateTemp, hasList := findAllVariablesFromState(state)
				if hasList {
					valueListState = append(valueListState, valueListStateTemp...)
				}

			}
		}

		objects := testDefinition.SelectElements("//ind:object")

		if len(objects) > 0 {
			for i := range objects {
				if objects[i] == nil {
					continue
				}

				object, ok := objectsTable[objects[i].SelectAttr("object_ref")]
				if !ok {
					continue
				}
				valueListObjectTemp, hasList := findAllVariablesFromObject(object)
				if hasList {
					valueListObject = append(valueListState, valueListObjectTemp...)
				}

			}
		}

		if len(valueListState) > 0 {
			valueList = append(valueList, valueListState...)

		}
		if len(valueListObject) > 0 {
			valueList = append(valueList, valueListObject...)
		}
		if len(valueList) > 0 {
			table[testId] = valueList
		}
	}

	return table
}

func findAllVariablesFromState(node *xmlquery.Node) ([]string, bool) {
	var valueList []string
	nodes := node.SelectElements("*")

	for i := range nodes {
		if nodes[i].SelectAttr("var_ref") != "" {
			dnsFriendlyFixId := strings.ReplaceAll(nodes[i].SelectAttr("var_ref"), "_", "-")
			valueFormatted := strings.TrimPrefix(dnsFriendlyFixId, ovalCheckPrefix)
			valueFormatted = strings.TrimSuffix(valueFormatted, ruleValueSuffix)
			valueList = append(valueList, valueFormatted)
		}
	}
	if len(valueList) > 0 {
		return valueList, true
	} else {
		return valueList, false
	}

}

func findAllVariablesFromObject(node *xmlquery.Node) ([]string, bool) {
	var valueList []string
	nodes := node.SelectElements("//ind:var_ref")
	for i := range nodes {
		if nodes[i].InnerText() != "" {
			dnsFriendlyFixId := strings.ReplaceAll(nodes[i].InnerText(), "_", "-")
			valueFormatted := strings.TrimPrefix(dnsFriendlyFixId, ovalCheckPrefix)
			valueFormatted = strings.TrimSuffix(valueFormatted, ruleValueSuffix)
			valueList = append(valueList, valueFormatted)
		}
	}
	if len(valueList) > 0 {
		return valueList, true
	} else {
		return valueList, false
	}
}

func GetRuleOvalTest(rule *xmlquery.Node, defTable NodeByIdHashTable) NodeByIdHashTable {
	var ovalRefEl *xmlquery.Node
	testList := make(map[string]*xmlquery.Node)
	for _, check := range rule.SelectElements("//xccdf-1.2:check") {
		if check.SelectAttr("system") == ovalCheckType {
			ovalRefEl = check.SelectElement("xccdf-1.2:check-content-ref")
			break
		}
	}

	if ovalRefEl == nil {
		return testList
	}

	ovalCheckName := strings.TrimSpace(ovalRefEl.SelectAttr("name"))
	ovalTest, ok := defTable[ovalCheckName]
	if !ok {
		return testList
	}

	ovalTests := ovalTest.SelectElements("//oval-def:criterion")
	for i := range ovalTests {
		if ovalTests[i].SelectAttr("test_ref") == "" {
			continue
		}
		testList[ovalTests[i].SelectAttr("test_ref")] = ovalTests[i]
	}

	return testList

}

func removeDuplicate(input []string) []string {
	keys := make(map[string]bool)
	trimmedList := []string{}

	for _, e := range input {
		if _, value := keys[e]; !value {
			keys[e] = true
			trimmedList = append(trimmedList, e)
		}
	}
	return trimmedList
}
func getValueListUsedForRule(rule *xmlquery.Node, ovalTable nodeByIdHashVariablesTable, defTable NodeByIdHashTable, variableList map[string]string) []string {
	var valueList []string
	ruleTests := GetRuleOvalTest(rule, defTable)
	if len(ruleTests) == 0 {
		return valueList
	}
	for test := range ruleTests {
		valueListTemp, ok := ovalTable[test]
		if !ok {
			continue
		}
		valueList = append(valueList, valueListTemp...)

	}
	if len(valueList) == 0 {
		return valueList
	}
	valueList = removeDuplicate(valueList)
	//remove duplicate because one rule can have different tests that use same variable, so we want to remove the extra variable since we
	//want to associate rule with value not specify check
	valueList = sort.StringSlice(valueList)
	var settableValueList []string
	for i := range valueList {
		if _, ok := variableList[strings.ReplaceAll(valueList[i], "-", "_")]; ok {
			settableValueList = append(settableValueList, valueList[i])
		}
	}

	return settableValueList
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

func GetInstructionsForRule(rule *xmlquery.Node, ocilTable NodeByIdHashTable) string {
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
	allValues := xmlquery.Find(resultsDom, "//set-value")
	valuesList := make(map[string]string)

	for _, codeNode := range allValues {
		valuesList[strings.TrimPrefix(codeNode.SelectAttr("idref"), valuePrefix)] = codeNode.InnerText()
	}

	ruleTable := newRuleHashTable(dsDom)
	questionsTable := NewOcilQuestionTable(dsDom)
	statesTable := newStateHashTable(dsDom)
	objsTable := newObjHashTable(dsDom)
	defTable := NewDefHashTable(dsDom)
	ovalTestVarTable := newValueListTable(dsDom, statesTable, objsTable)
	results := resultsDom.SelectElements("//rule-result")
	parsedResults := make([]*ParseResult, 0)
	var remErrs string
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

		instructions := GetInstructionsForRule(resultRule, questionsTable)
		ruleValues := getValueListUsedForRule(resultRule, ovalTestVarTable, defTable, valuesList)
		resCheck, err := newComplianceCheckResult(result, resultRule, ruleIDRef, instructions, scanName, namespace, ruleValues)
		if err != nil {
			continue
		}

		if resCheck != nil {
			pr := &ParseResult{
				Id:          ruleIDRef,
				CheckResult: resCheck,
			}
			pr.Remediations, err = newComplianceRemediation(scheme, scanName, namespace, resultRule, valuesList)
			if err != nil {
				remErrs = "CheckID." + ruleIDRef + err.Error() + "\n"
			}
			parsedResults = append(parsedResults, pr)
		}
	}
	if remErrs != "" {
		return parsedResults, errors.New(remErrs)
	}
	return parsedResults, nil

}

// Returns a new complianceCheckResult if the check data is usable
func newComplianceCheckResult(result *xmlquery.Node, rule *xmlquery.Node, ruleIdRef, instructions, scanName, namespace string, ruleValues []string) (*compv1alpha1.ComplianceCheckResult, error) {
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
		Warnings:     GetWarningsForRule(rule),
		ValuesUsed:   ruleValues,
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

func GetWarningsForRule(rule *xmlquery.Node) []string {
	warningObjs := rule.SelectElements("//xccdf-1.2:warning")

	warnings := []string{}

	for _, warn := range warningObjs {
		if warn == nil {
			continue
		}
		// We skip this warning if it's relevant
		// to parsing the API paths.
		if warningHasApiObjects(warn) {
			continue
		}
		warnings = append(warnings, XmlNodeAsMarkdown(warn))
	}

	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func RuleHasApiObjectWarning(rule *xmlquery.Node) bool {
	warningObjs := rule.SelectElements("//xccdf-1.2:warning")

	for _, warn := range warningObjs {
		if warn == nil {
			continue
		}
		if warningHasApiObjects(warn) {
			return true
		}
	}

	return false
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

func newComplianceRemediation(scheme *runtime.Scheme, scanName, namespace string, rule *xmlquery.Node, resultValues map[string]string) ([]*compv1alpha1.ComplianceRemediation, error) {
	for _, fix := range rule.SelectElements("//xccdf-1.2:fix") {
		if isRelevantFix(fix) {
			return remediationFromFixElement(scheme, fix, scanName, namespace, resultValues)
		}
	}

	return nil, nil
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

func remediationFromFixElement(scheme *runtime.Scheme, fix *xmlquery.Node, scanName, namespace string, resultValues map[string]string) ([]*compv1alpha1.ComplianceRemediation, error) {
	fixId := fix.SelectAttr("id")
	if fixId == "" {
		return nil, errors.New("there is no fix-ID attribute")
	}

	dnsFriendlyFixId := strings.ReplaceAll(fixId, "_", "-")
	remName := fmt.Sprintf("%s-%s", scanName, dnsFriendlyFixId)
	// TODO(OZZ) fix text
	return remediationsFromString(scheme, remName, namespace, fix.InnerText(), resultValues)
}

func remediationsFromString(scheme *runtime.Scheme, name string, namespace string, fixContent string, resultValues map[string]string) ([]*compv1alpha1.ComplianceRemediation, error) {
	//ToDO find and substitute the value
	fixWithValue, valuesUsedList, notFoundValueList, parsingError := parseValues(fixContent, resultValues)
	if parsingError != nil {
		return nil, parsingError
	}

	objs, err := ReadObjectsFromYAML(strings.NewReader(fixWithValue))
	if err != nil {
		return nil, err
	}
	rems := make([]*compv1alpha1.ComplianceRemediation, 0, len(objs))
	for idx := range objs {
		obj := objs[idx]
		annotations := make(map[string]string)

		if len(notFoundValueList) > 0 {
			annotations = handleNotFoundValue(notFoundValueList, annotations)
		}
		if len(valuesUsedList) > 0 {
			annotations = handleValueUsed(valuesUsedList, annotations)
		}

		if hasValueRequiredAnnotation(obj) {
			if (len(notFoundValueList) == 0) && (len(valuesUsedList) == 0) {
				return nil, errors.New("do not have any parsed xccdf variable, shoudn't any required values")
			} else {
				annotations = handleValueRequiredAnnotation(obj, annotations)
			}
		}

		if hasDependencyAnnotation(obj) {
			annotations = handleDependencyAnnotation(obj, annotations)
		}

		if hasOptionalAnnotation(obj) {
			annotations = handleOptionalAnnotation(obj, annotations)
		}

		remType := compv1alpha1.ConfigurationRemediation
		if hasTypeAnnotation(obj) {
			remType = handleRemediationTypeAnnotation(obj)
		}

		if remType == compv1alpha1.EnforcementRemediation &&
			hasEnforcementTypeAnnotation(obj) {
			annotations = handleEnforcementTypeAnnotation(obj, annotations)
		}

		var remName string
		if idx == 0 {
			// Use result's name
			remName = name
		} else {
			remName = fmt.Sprintf("%s-%d", name, idx)
		}

		rems = append(rems, &compv1alpha1.ComplianceRemediation{
			ObjectMeta: v1.ObjectMeta{
				Name:        remName,
				Namespace:   namespace,
				Annotations: annotations,
			},
			Spec: compv1alpha1.ComplianceRemediationSpec{
				ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
					Apply: false,
					Type:  remType,
				},
				Current: compv1alpha1.ComplianceRemediationPayload{
					Object: obj,
				},
			},
			Status: compv1alpha1.ComplianceRemediationStatus{
				ApplicationState: compv1alpha1.RemediationPending,
			},
		})
	}

	return rems, nil
}

func toArrayByComma(format string) []string {
	return strings.Split(format, ",")
}

//This function will take orginal remediation content, and a list of all values found in the configMap
//It will processed and substitue the value in remediation content, and return processed Remediation content
//The return will be Processed-Remdiation Content, Value-Used List, Un-Set List, and err if possible
func parseValues(remContent string, resultValues map[string]string) (string, []string, []string, error) {
	var valuesUsedList []string
	var valuesMissingList []string
	//find everything start and end with {{}}
	re := regexp.MustCompile(`\{\{[^}]*\}\}`)
	contentList := re.FindAllString(remContent, -1)
	fixedText := remContent
	if len(contentList) == 0 {
		return remContent, valuesUsedList, valuesMissingList, nil
	}
	// there are two types of content we need to process, one is url-encoded machine config source ex. {{ -a%20always0-F%20di }},
	// the other one is not url_encoded ex. {{.var_some_value}}, we are going to take care of url-encoded content first, and then
	// feed the processed content to template again

	for _, content := range contentList {

		// take out `{{ `,' }}' out from content
		trimmedContent := content[trimStartIndex:][:len(content)-trimStartIndex-trimEndIndex]
		// take out leading and tailling spaces
		trimmedContent = strings.TrimSpace(trimmedContent)

		// trimmedContent here should only contain url-encoded content, check if it contains illegall character space
		isIllegalURL := regexp.MustCompile(".*[\\ \"\\<\\>\\{\\}|\\\\^~\\[\\]].*")
		if isIllegalURL.MatchString(trimmedContent) {
			continue
		}

		var decodeErr error
		preProcessedContent, decodeErr := url.QueryUnescape(trimmedContent)
		if decodeErr != nil {
			return remContent, valuesUsedList, valuesMissingList, errors.Wrap(decodeErr, "error while decode remediation context: ")
		}

		// we don't need special processing if preProcessedContent is same as orginal content
		if preProcessedContent == trimmedContent {
			continue
		}

		fixedContent, usedVals, missingVals, err := processContent(preProcessedContent, resultValues)
		if err != nil {
			return remContent, valuesUsedList, valuesMissingList, errors.Wrap(err, "error while processing remediation context: ")
		}
		valuesUsedList = append(valuesUsedList, usedVals...)
		valuesMissingList = append(valuesMissingList, missingVals...)
		fixedText = strings.ReplaceAll(fixedText, content, url.PathEscape(fixedContent))

	}

	// now the content is free of url-encoded string, we can feed the fixedContent to template to process the general case content.
	// ex. {{.<variable name>}}
	fixedText, usedVals, missingVals, err := processContent(fixedText, resultValues)
	if err != nil {
		return remContent, valuesUsedList, valuesMissingList, errors.Wrap(err, "error while processing remediation context: ")
	}
	valuesUsedList = append(valuesUsedList, usedVals...)
	valuesMissingList = append(valuesMissingList, missingVals...)

	return fixedText, valuesUsedList, valuesMissingList, nil
}

func processContent(preProcessedContent string, resultValues map[string]string) (string, []string, []string, error) {
	var valuesUsedList []string
	var valuesMissingList []string
	var valuesParsedList []string
	t, err := template.New("").Option("missingkey=zero").Funcs(template.FuncMap{"toArrayByComma": toArrayByComma}).
		Parse(preProcessedContent)
	if err != nil {
		return preProcessedContent, valuesUsedList, valuesMissingList, errors.Wrap(err, "wrongly formatted remediation context: ") //Error creating template // Wrongly formatted remediation context
	}

	buf := &bytes.Buffer{}
	err = t.Execute(buf, resultValues)
	if err != nil {
		return preProcessedContent, valuesUsedList, valuesMissingList, errors.Wrap(err, "error while parsing variables into values: ")
	}
	fixedContent := buf.String()

	//Iterate through template tree to get all parsed variable
	valuesParsedList = getParsedValueName(t)
	for _, parsedVariable := range valuesParsedList {
		_, found := resultValues[parsedVariable]
		if found {
			dnsFriendlyParsedVariable := strings.ReplaceAll(parsedVariable, "_", "-")
			valuesUsedList = append(valuesUsedList, dnsFriendlyParsedVariable)
		} else {
			dnsFriendlyParsedVariable := strings.ReplaceAll(parsedVariable, "_", "-")
			valuesMissingList = append(valuesMissingList, dnsFriendlyParsedVariable)
		}
	}
	return fixedContent, valuesUsedList, valuesMissingList, nil
}

func getParsedValueName(t *template.Template) []string {
	valueToBeTrimmed := listNodeFields(t.Tree.Root, nil)
	return trimToValue(valueToBeTrimmed)
}

//trim {{value | urlquery}} list to value list
func trimToValue(listToBeTrimmed []string) []string {
	trimmedValuesList := listToBeTrimmed[:0]
	for _, oriVal := range listToBeTrimmed {
		re := regexp.MustCompile("([a-zA-Z-0-9]+(_[a-zA-Z-0-9]+)+)")
		trimedValueMatch := re.FindStringSubmatch(oriVal)
		if len(trimedValueMatch) > 1 {
			trimmedValuesList = append(trimmedValuesList, trimedValueMatch[0])
		}
	}
	return trimmedValuesList
}

func listNodeFields(node parse.Node, res []string) []string {
	if node.Type() == parse.NodeAction {
		res = append(res, node.String())
	}

	if ln, ok := node.(*parse.ListNode); ok {
		for _, n := range ln.Nodes {
			res = listNodeFields(n, res)
		}
	}
	return res
}

func hasDependencyAnnotation(u *unstructured.Unstructured) bool {
	return hasAnnotation(u, dependencyAnnotationKey) || hasAnnotation(u, kubeDependencyAnnotationKey)
}

func hasValueRequiredAnnotation(u *unstructured.Unstructured) bool {
	return hasAnnotation(u, valueInputRequiredAnnotationKey)
}

func hasOptionalAnnotation(u *unstructured.Unstructured) bool {
	return hasAnnotation(u, optionalAnnotationKey)
}

func hasTypeAnnotation(u *unstructured.Unstructured) bool {
	return hasAnnotation(u, remediationTypeAnnotationKey)
}

func hasEnforcementTypeAnnotation(u *unstructured.Unstructured) bool {
	return hasAnnotation(u, enforcementTypeAnnotationKey)
}

func hasAnnotation(u *unstructured.Unstructured, annotation string) bool {
	annotations := u.GetAnnotations()
	if annotations == nil {
		return false
	}

	_, hasAnn := annotations[annotation]
	return hasAnn
}

func handleDependencyAnnotation(u *unstructured.Unstructured, annotations map[string]string) map[string]string {
	// We already assume this has some annotation
	inAnns := u.GetAnnotations()

	// parse

	if dependencies, hasDepKey := inAnns[dependencyAnnotationKey]; hasDepKey {
		// set dependencies
		annotations[compv1alpha1.RemediationDependencyAnnotation] = dependencies

		// reset metadata of output object
		delete(inAnns, dependencyAnnotationKey)
	}

	if objDeps, hasKubeDepKey := inAnns[kubeDependencyAnnotationKey]; hasKubeDepKey {
		// set dependencies
		annotations[compv1alpha1.RemediationObjectDependencyAnnotation] = objDeps

		// reset metadata of output object
		delete(inAnns, kubeDependencyAnnotationKey)
	}

	u.SetAnnotations(inAnns)

	return annotations
}

func handleValueUsed(valuesList []string, annotations map[string]string) map[string]string {

	annotations[compv1alpha1.RemediationValueUsedAnnotation] = strings.Join(valuesList, ",")

	return annotations
}

func handleNotFoundValue(notFoundValues []string, annotations map[string]string) map[string]string {

	annotations[compv1alpha1.RemediationUnsetValueAnnotation] = strings.Join(notFoundValues, ",")
	return annotations
}

func handleValueRequiredAnnotation(u *unstructured.Unstructured, annotations map[string]string) map[string]string {
	// We already assume this has some annotation
	inAnns := u.GetAnnotations()

	// parse
	if valueRequired, hasValueReqKey := inAnns[valueInputRequiredAnnotationKey]; hasValueReqKey {
		// set required custom variable names
		dnsFriendlyRequiredVariable := strings.ReplaceAll(valueRequired, "_", "-")
		annotations[compv1alpha1.RemediationValueRequiredAnnotation] = dnsFriendlyRequiredVariable

		// reset metadata of output object
		delete(inAnns, valueInputRequiredAnnotationKey)
	}

	u.SetAnnotations(inAnns)

	return annotations
}

func handleOptionalAnnotation(u *unstructured.Unstructured, annotations map[string]string) map[string]string {
	// We already assume this has some annotation
	inAnns := u.GetAnnotations()

	// parse

	if _, hasKey := inAnns[optionalAnnotationKey]; hasKey {
		// set dependencies
		annotations[compv1alpha1.RemediationOptionalAnnotation] = ""

		// reset metadata of output object
		delete(inAnns, optionalAnnotationKey)
	}

	u.SetAnnotations(inAnns)

	return annotations
}

func handleRemediationTypeAnnotation(u *unstructured.Unstructured) compv1alpha1.RemediationType {
	// We already assume this has some annotation
	inAnns := u.GetAnnotations()

	// parse
	remType := inAnns[remediationTypeAnnotationKey]
	// reset metadata of output object
	delete(inAnns, enforcementTypeAnnotationKey)

	u.SetAnnotations(inAnns)
	return compv1alpha1.RemediationType(remType)
}

func handleEnforcementTypeAnnotation(u *unstructured.Unstructured, annotations map[string]string) map[string]string {
	// We already assume this has some annotation
	inAnns := u.GetAnnotations()

	// parse
	typeAnn, hasKey := inAnns[enforcementTypeAnnotationKey]
	if hasKey {
		// set dependencies
		annotations[compv1alpha1.RemediationEnforcementTypeAnnotation] = typeAnn

		// reset metadata of output object
		delete(inAnns, enforcementTypeAnnotationKey)
	}

	u.SetAnnotations(inAnns)

	return annotations
}
