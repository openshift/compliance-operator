package utils

import (
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
)

// XMLDocument is a wrapper that keeps the interface XML-parser-agnostic
type XMLDocument struct {
	*xmldom.Document
}

// ParseContent parses the DataStream and returns the XML document
func ParseContent(dsReader io.Reader) (*XMLDocument, error) {
	dsDom, err := xmldom.Parse(dsReader)
	if err != nil {
		return nil, err
	}
	return &XMLDocument{dsDom}, nil
}

// ParseRemediationFromContentAndResults parses the content DS and the results from the scan, and generates
// the necessary remediations
func ParseRemediationFromContentAndResults(scheme *runtime.Scheme, scanName string, namespace string,
	dsDom *XMLDocument, resultsReader io.Reader) ([]*compv1alpha1.ComplianceRemediation, error) {
	remediations := make([]*compv1alpha1.ComplianceRemediation, 0)

	resultsDom, err := xmldom.Parse(resultsReader)
	if err != nil {
		return nil, err
	}

	// Get the checks that had failed
	failedRuleResults := filterFailedResults(resultsDom.Root.Query("//rule-result"))

	// Get group that contains remediations
	remediationsDom := dsDom.Root.QueryOne("//component/Benchmark")
	for _, frr := range failedRuleResults {
		// Each result has the rule ID in the idref attribute
		ruleIDRef := frr.GetAttributeValue("idref")
		if ruleIDRef == "" {
			continue
		}

		ruleDefinition := remediationsDom.FindByID(ruleIDRef)
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
	if fix.GetAttributeValue("system") == machineConfigFixType {
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
