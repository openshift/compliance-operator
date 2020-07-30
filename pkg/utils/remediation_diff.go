package utils

import (
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"math"
)

// ParseResultContextItem wraps ParseResult with some metadata that need to be added
// to the created k8s object based on the processing result as well as which nodes
// the result comes from and whether it's been processed during a single loop
// that processes a single CM yet or not. The sources are used to keep track of
// which nodes differ from the "canonical" state of the check
type ParseResultContextItem struct {
	ParseResult

	Annotations map[string]string
	Labels      map[string]string

	sources   []string
	processed bool
}

func newParseResultWithSources(pr *ParseResult, sources ...string) *ParseResultContextItem {
	return &ParseResultContextItem{
		ParseResult: ParseResult{
			// We explicitly DeepCopy the CheckResult and the Remediation so that we don't
			// hold any references to the slice of the original ParseResults and the slice
			// can be garbage-collected
			Id:          pr.Id,
			CheckResult: pr.CheckResult.DeepCopy(),
			Remediation: pr.Remediation.DeepCopy(),
		},
		sources:   sources,
		processed: false,
	}
}

// ParseResultContext keeps track of items that are consistent across all
// "sources" in a ComplianceScan as well as items that are inconsistent
type ParseResultContext struct {
	consistent   map[string]*ParseResultContextItem
	inconsistent map[string][]*ParseResultContextItem
}

func NewParseResultContext() *ParseResultContext {
	return &ParseResultContext{
		consistent:   make(map[string]*ParseResultContextItem),
		inconsistent: make(map[string][]*ParseResultContextItem),
	}
}

// ParseResultContext.AddResults adds a batch of results coming from the parser and partitions them into
// either the consistent or the inconsistent list
func (prCtx *ParseResultContext) AddResults(source string, parsedResList []*ParseResult) {
	// If there is no source, the configMap is probably a platform scan map, in that case
	// treat all the results as consistent.
	if source == "" {
		prCtx.addConsistentResults(source, parsedResList)
		return
	}

	// Treat the first batch of results as consistent
	if len(prCtx.inconsistent) == 0 && len(prCtx.consistent) == 0 {
		prCtx.addConsistentResults(source, parsedResList)
	} else {
		prCtx.addParsedResults(source, parsedResList)
	}
}

func (prCtx *ParseResultContext) addConsistentResults(source string, parsedResList []*ParseResult) {
	for _, parsedRes := range parsedResList {
		prCtx.consistent[parsedRes.Id] = newParseResultWithSources(parsedRes, source)
	}
}

func (prCtx *ParseResultContext) addInconsistentResult(id string, pr *ParseResult, sources ...string) {
	_, ok := prCtx.inconsistent[id]
	if !ok {
		prCtx.inconsistent[id] = []*ParseResultContextItem{
			newParseResultWithSources(pr, sources...),
		}
		return
	}

	prCtx.inconsistent[id] = append(prCtx.inconsistent[id], newParseResultWithSources(pr, sources...))
}

// ParseResultContext.addParsedResults add a subsequent batch of results that must be examined
// for consistency
func (prCtx *ParseResultContext) addParsedResults(source string, newResults []*ParseResult) {
	for _, consistentResult := range prCtx.consistent {
		consistentResult.processed = false
	}

	for _, pr := range newResults {
		consistentPr, ok := prCtx.consistent[pr.Id]
		if !ok {
			// This either already inconsistent result or an extra
			// this batch has an extra item, save it as a diff with (only so far) this source
			prCtx.addInconsistentResult(pr.Id, pr, source)
			continue
		}
		consistentPr.processed = true

		ok = diffChecks(consistentPr.CheckResult, pr.CheckResult) && diffRemediations(consistentPr.Remediation, pr.Remediation)
		if !ok {
			// remove the check from consistent, add it to diff, but TWICE
			// once for the sources from the consistent list and once for the new source
			prCtx.addInconsistentResult(pr.Id, &consistentPr.ParseResult, consistentPr.sources...)
			delete(prCtx.consistent, pr.Id)
			prCtx.addInconsistentResult(pr.Id, pr, source)
			continue
		}

		// OK, same as a previous result in consistent, just append the source
		consistentPr.sources = append(consistentPr.sources, source)
	}

	// Make sure all previously consistent items were touched, IOW we didn't receive
	// fewer items by moving all previously untouched items to the inconsistent list
	for _, consistentResult := range prCtx.consistent {
		if consistentResult.processed == true {
			continue
		}
		// Deleting an item from a map while iterating over it is safe, see https://golang.org/doc/effective_go.html#for
		prCtx.addInconsistentResult(consistentResult.Id, &consistentResult.ParseResult, consistentResult.sources...)
		delete(prCtx.consistent, consistentResult.Id)
	}
}

// ParseResultContext.ReconcileInconsistentResults interates through all inconsistent results
// and tries to reconcile them, creating a single consistent ParseResultContextItem for each
func (prCtx *ParseResultContext) reconcileInconsistentResults() {
	for id, inconsistentResultList := range prCtx.inconsistent {

		if len(inconsistentResultList) < 1 {
			continue
		}

		reconciled := reconcileInconsistentResult(inconsistentResultList)
		if _, ok := prCtx.consistent[id]; ok {
			reconciled.Remediation = nil
			reconciled.CheckResult.Status = compv1alpha1.CheckResultError
			reconciled.Annotations = annotateErrorStatus("Check found in both consistent and inconsistent lists")
			reconciled.Remediation = nil
		}
		prCtx.consistent[id] = reconciled
	}
}

func (prCtx *ParseResultContext) GetConsistentResults() []*ParseResultContextItem {
	prCtx.reconcileInconsistentResults()

	consistentList := make([]*ParseResultContextItem, 0)

	for _, item := range prCtx.consistent {
		consistentList = append(consistentList, item)
	}

	return consistentList
}

func reconcileInconsistentResult(inconsistent []*ParseResultContextItem) *ParseResultContextItem {
	var createRemediations bool

	if len(inconsistent) < 0 {
		return nil
	}

	pr := ParseResultContextItem{
		ParseResult: ParseResult{
			Id:          inconsistent[0].Id,
			CheckResult: inconsistent[0].CheckResult.DeepCopy(),
			Remediation: inconsistent[0].Remediation.DeepCopy(),
		},
	}

	isDifferent, diffMsg := differsExceptStatus(inconsistent)
	if isDifferent {
		pr.CheckResult.Status = compv1alpha1.CheckResultError
		pr.Annotations = annotateErrorStatus("Check sources differ in more than status\n" + diffMsg)
		pr.Remediation = nil
	} else {
		pr.CheckResult.Status = compv1alpha1.CheckResultInconsistent
		pr.Annotations, createRemediations = annotateInconsistentStatuses(inconsistent)
		if !createRemediations {
			pr.Remediation = nil
		}
	}

	pr.Labels = make(map[string]string)
	pr.Labels[compv1alpha1.ComplianceCheckInconsistentLabel] = ""

	return &pr
}

func differsExceptStatus(inconsistent []*ParseResultContextItem) (bool, string) {
	if len(inconsistent) < 2 {
		return false, ""
	}
	base := inconsistent[0]

	for _, item := range inconsistent[1:] {
		ok := cmp.Equal(base, item,
			cmpopts.IgnoreTypes(compv1alpha1.ComplianceCheckResult{}),
			cmpopts.IgnoreUnexported(ParseResultContextItem{}))
		if !ok {
			diff := cmp.Diff(base, item,
				cmpopts.IgnoreTypes(compv1alpha1.ComplianceCheckResult{}),
				cmpopts.IgnoreUnexported(ParseResultContextItem{}))
			return true, diff
		}
	}

	return false, ""
}

func annotateErrorStatus(msg string) map[string]string {
	annotations := make(map[string]string)
	annotations[compv1alpha1.ComplianceCheckResultErrorAnnotation] = msg
	return annotations
}

func annotateInconsistentStatuses(inconsistent []*ParseResultContextItem) (map[string]string, bool) {
	mostCommonState, hasCommonState := mostCommonState(inconsistent)
	createRemediation := true

	annotations := make(map[string]string)
	for _, check := range inconsistent {
		// We'll only create remediations for inconsistent result that contain pass,fail or info
		// as they still can be remediatied
		switch check.CheckResult.Status {
		case compv1alpha1.CheckResultFail, compv1alpha1.CheckResultPass, compv1alpha1.CheckResultInfo:
			break
		default:
			createRemediation = false
		}

		if hasCommonState && check.CheckResult.Status == mostCommonState {
			continue
		}

		for _, src := range check.sources {
			curVal, ok := annotations[compv1alpha1.ComplianceCheckResultInconsistentSourceAnnotation]
			if !ok {
				annotations[compv1alpha1.ComplianceCheckResultInconsistentSourceAnnotation] = src + ":" + string(check.CheckResult.Status)
			} else {
				annotations[compv1alpha1.ComplianceCheckResultInconsistentSourceAnnotation] = curVal + "," + src + ":" + string(check.CheckResult.Status)
			}
		}
	}

	if hasCommonState {
		annotations[compv1alpha1.ComplianceCheckResultMostCommonAnnotation] = string(mostCommonState)
	}

	return annotations, createRemediation
}

func mostCommonState(inconsistent []*ParseResultContextItem) (compv1alpha1.ComplianceCheckStatus, bool) {
	statusCounter := make(map[compv1alpha1.ComplianceCheckStatus]int)
	for _, check := range inconsistent {
		statusCounter[check.CheckResult.Status] = statusCounter[check.CheckResult.Status] + len(check.sources)
	}

	mostCommonState := compv1alpha1.CheckResultError // let's default to something safe
	numCommonState := 0
	for state, num := range statusCounter {
		if num > numCommonState {
			mostCommonState = state
			numCommonState = num
		}
	}

	// We have a common state if at least 60% of checks agree on a result
	requiredNumCommonState := int(math.Ceil(float64(len(inconsistent)) * 0.6))
	hasCommonState := true
	if numCommonState < requiredNumCommonState {
		hasCommonState = false
	}

	return mostCommonState, hasCommonState
}

// returns true if the checks are the same, false if they differ
func diffChecks(old, new *compv1alpha1.ComplianceCheckResult) bool {
	if old == nil {
		return new == nil
	}

	// should we be more picky and just compare what can be set with the remediations? e.g. OSImageURL can't
	// be set with a remediation..
	return cmp.Equal(old, new)
}

// returns true if the remediations are the same, false if they differ
// for now (?) just diffs the MC specs and the remediation type, not sure if we'll ever want to diff more
func diffRemediations(old, new *compv1alpha1.ComplianceRemediation) bool {
	if old == nil {
		return new == nil
	}

	if old.Spec.Current.Object.GetKind() != new.Spec.Current.Object.GetKind() {
		return false
	}

	// should we be more picky and just compare what can be set with the remediations? e.g. OSImageURL can't
	// be set with a remediation..
	return cmp.Equal(old.Spec.Current.Object, new.Spec.Current.Object)
}
