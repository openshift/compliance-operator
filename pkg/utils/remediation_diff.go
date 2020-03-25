package utils

import (
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"reflect"
	"sort"
)

// returns true if the lists are the same, false if they differ
func DiffRemediationList(oldList, newList []*compv1alpha1.ComplianceRemediation) bool {
	if newList == nil {
		return oldList == nil
	}

	if len(newList) != len(oldList) {
		return false
	}

	sortMcSlice := func(mcSlice []*compv1alpha1.ComplianceRemediation) {
		sort.SliceStable(mcSlice, func(i, j int) bool { return mcSlice[i].Name < mcSlice[j].Name })
	}

	sortMcSlice(oldList)
	sortMcSlice(newList)

	for i := range oldList {
		ok := diffRemediations(oldList[i], newList[i])
		if !ok {
			return false
		}
	}

	return true
}

// returns true if the remediations are the same, false if they differ
// for now (?) just diffs the MC specs and the remediation type, not sure if we'll ever want to diff more
func diffRemediations(old, new *compv1alpha1.ComplianceRemediation) bool {
	if old.Spec.Type != new.Spec.Type {
		return false
	}

	// should we be more picky and just compare what can be set with the remediations? e.g. OSImageURL can't
	// be set with a remediation..
	return reflect.DeepEqual(old.Spec.MachineConfigContents.Spec, new.Spec.MachineConfigContents.Spec)
}
