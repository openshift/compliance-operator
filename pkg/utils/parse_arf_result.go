package utils

import (
	"fmt"
	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
	mcfgv1 "github.com/openshift/compliance-operator/pkg/apis/machineconfiguration/v1"
	"github.com/subchen/go-xmldom"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"strings"
)

const (
	// FIXME: we pretend that shell is MC..
	machineConfigFixType   = "urn:xccdf:fix:script:sh"
	machineConfigFixPrefix = "apiVersion: machineconfiguration.openshift.io/v1"
)

func ParseRemediationsFromArf(scheme *runtime.Scheme, scanName string, namespace string, arf string) ([]*complianceoperatorv1alpha1.ComplianceRemediation, error) {
	remediations := make([]*complianceoperatorv1alpha1.ComplianceRemediation, 0)

	reader := strings.NewReader(arf)
	dom, err := xmldom.Parse(reader)
	if err != nil {
		return nil, err
	}

	// Get the checks that had failed
	failedRuleResults := filterFailedResults(dom.Root.Query("//TestResult/rule-result"))

	// For each failed result, find the remediation
	for _, frr := range failedRuleResults {
		// Each result has the rule ID in the idref attribute
		ruleIdRef := frr.GetAttributeValue("idref")
		if ruleIdRef == "" {
			continue
		}

		ruleDefinition := dom.Root.FindByID(ruleIdRef)
		if ruleDefinition == nil {
			continue
		}

		for _, fix := range ruleDefinition.FindByName("fix") {
			if !isMachineConfigFix(fix) {
				continue
			}

			newRemediation := remediationFromFixElement(scheme, fix, scanName, namespace)
			if newRemediation == nil {
				continue
			}
			remediations = append(remediations, newRemediation)
		}
	}

	return remediations, nil
}

func isMachineConfigFix(fix *xmldom.Node) bool {
	if fix.GetAttributeValue("system") == machineConfigFixType && strings.HasPrefix(fix.Text, machineConfigFixPrefix) {
		return true
	}
	return false
}

func filterFailedResults(results []*xmldom.Node) []*xmldom.Node {
	failed := make([]*xmldom.Node, 0)

	for _, res := range results {
		resultEl := res.FindOneByName("result")
		if resultEl.Text == "fail" {
			failed = append(failed, res)
		}
	}

	return failed
}

func remediationFromFixElement(scheme *runtime.Scheme, fix *xmldom.Node, scanName, namespace string) *complianceoperatorv1alpha1.ComplianceRemediation {
	fixId := fix.GetAttributeValue("id")
	if fixId == "" {
		return nil
	}

	dnsFriendlyFixId := strings.ReplaceAll(fixId, "_", "-")
	remName := fmt.Sprintf("%s-%s", scanName, dnsFriendlyFixId)
	return remediationFromString(scheme, remName, namespace, fix.Text)
}

func remediationFromString(scheme *runtime.Scheme, name string, namespace string, mcContent string) *complianceoperatorv1alpha1.ComplianceRemediation {
	mcObject, err := rawObjectToMachineConfig(scheme, []byte(mcContent))
	if err != nil {
		return nil
	}

	return &complianceoperatorv1alpha1.ComplianceRemediation{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: complianceoperatorv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: complianceoperatorv1alpha1.ComplianceRemediationSpecMeta{
				Type:  complianceoperatorv1alpha1.McRemediation,
				Apply: false,
			},
			MachineConfigContents: *mcObject,
		},
		Status: complianceoperatorv1alpha1.ComplianceRemediationStatus{
			ApplicationState: complianceoperatorv1alpha1.RemediationNotSelected,
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
