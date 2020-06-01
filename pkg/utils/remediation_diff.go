package utils

import (
	"github.com/google/go-cmp/cmp"
	"reflect"
	"sort"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

// returns true if the lists are the same, false if they differ
func DiffRemediationList(oldList, newList []*ParseResult) (bool, string) {
	var res bool
	var stringDiff string

	if newList == nil {
		res = oldList == nil
		return res, "At least one of the lists was null"
	}

	if len(newList) != len(oldList) {
		return false, "Lists had different length"
	}

	sortMcSlice := func(parseResultSlice []*ParseResult) {
		sort.SliceStable(parseResultSlice, func(i, j int) bool {
			return parseResultSlice[i].CheckResult.Name < parseResultSlice[j].CheckResult.Name
		})
	}

	sortMcSlice(oldList)
	sortMcSlice(newList)

	res = true
	for i := range oldList {
		ok := diffChecks(oldList[i].CheckResult, newList[i].CheckResult)
		if !ok {
			stringDiff += cmp.Diff(oldList[i].CheckResult, newList[i].CheckResult)
			res = false
		}

		ok = diffRemediations(oldList[i].Remediation, newList[i].Remediation)
		if !ok {
			stringDiff += cmp.Diff(oldList[i].Remediation, newList[i].Remediation)
			res = false
		}
	}

	return res, stringDiff
}

// returns true if the checks are the same, false if they differ
func diffChecks(old, new *compv1alpha1.ComplianceCheckResult) bool {
	if old == nil {
		return new == nil
	}

	// should we be more picky and just compare what can be set with the remediations? e.g. OSImageURL can't
	// be set with a remediation..
	return reflect.DeepEqual(old, new)
}

// returns true if the remediations are the same, false if they differ
// for now (?) just diffs the MC specs and the remediation type, not sure if we'll ever want to diff more
func diffRemediations(old, new *compv1alpha1.ComplianceRemediation) bool {
	if old == nil {
		return new == nil
	}

	if old.Spec.Object.GetKind() != new.Spec.Object.GetKind() {
		return false
	}

	// should we be more picky and just compare what can be set with the remediations? e.g. OSImageURL can't
	// be set with a remediation..
	return reflect.DeepEqual(old.Spec.Object, new.Spec.Object)
}
