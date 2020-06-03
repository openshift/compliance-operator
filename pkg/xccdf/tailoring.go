package xccdf

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

const (
	// XMLHeader is the header for the XML doc
	XMLHeader       string = `<?xml version="1.0" encoding="UTF-8"?>`
	profileIDPrefix string = "xccdf_org.ssgproject.content_profile_"
	ruleIDPrefix    string = "xccdf_org.ssgproject.content_rule_"
	varIDPrefix     string = "xccdf_org.ssgproject.content_value_"
	// XCCDFNamespace is the XCCDF namespace of this project. Per the XCCDF
	// specification, this assiciates the content with the author
	XCCDFNamespace string = "compliance.openshift.io"
	XCCDFURI       string = "http://checklists.nist.gov/xccdf/1.2"
)

type TailoringElement struct {
	XMLName         xml.Name `xml:"xccdf-1.2:Tailoring"`
	XMLNamespaceURI string   `xml:"xmlns:xccdf-1.2,attr"`
	ID              string   `xml:"id,attr"`
	Benchmark       BenchmarkElement
	Version         VersionElement
	Profile         ProfileElement
	// TODO(jaosorior): Add signature capabilities
	// Signature SignatureElement
}

type BenchmarkElement struct {
	XMLName xml.Name `xml:"xccdf-1.2:benchmark"`
	Href    string   `xml:"href,attr"`
}

type VersionElement struct {
	XMLName xml.Name `xml:"xccdf-1.2:version"`
	// FIXME(jaosorior): time.Time doesn't satisfy the unmarshalling
	// interface needed by the XML library in golang. I used a string
	// instead cause I was lazy.
	Time  string `xml:"time,attr"`
	Value string `xml:",chardata"`
}

type ProfileElement struct {
	XMLName     xml.Name                   `xml:"xccdf-1.2:Profile"`
	ID          string                     `xml:"id,attr"`
	Extends     string                     `xml:"extends,attr"`
	Title       *TitleOrDescriptionElement `xml:"xccdf-1.2:title,omitempty"`
	Description *TitleOrDescriptionElement `xml:"xccdf-1.2:description,omitempty"`
	Selections  []SelectElement
	Values      []SetValueElement
}

type TitleOrDescriptionElement struct {
	Override bool   `xml:"override,attr"`
	Value    string `xml:",chardata"`
}

type SelectElement struct {
	XMLName  xml.Name `xml:"xccdf-1.2:select"`
	IDRef    string   `xml:"idref,attr"`
	Selected bool     `xml:"selected,attr"`
}

type SetValueElement struct {
	XMLName xml.Name `xml:"xccdf-1.2:set-value"`
	IDRef   string   `xml:"idref,attr"`
	Value   string   `xml:",chardata"`
}

// GetXCCDFProfileID gets a profile xccdf ID from the TailoredProfile object
func GetXCCDFProfileID(tp *cmpv1alpha1.TailoredProfile) string {
	return fmt.Sprintf("xccdf_%s_profile_%s", XCCDFNamespace, tp.Name)
}

// GetProfileNameFromID gets a profile name from the xccdf ID
func GetProfileNameFromID(id string) string {
	trimedName := strings.TrimPrefix(id, profileIDPrefix)
	return strings.ToLower(strings.ReplaceAll(trimedName, "_", "-"))
}

// GetRuleNameFromID gets a rule name from the xccdf ID
func GetRuleNameFromID(id string) string {
	trimedName := strings.TrimPrefix(id, ruleIDPrefix)
	return strings.ToLower(strings.ReplaceAll(trimedName, "_", "-"))
}

func GetVariableNameFromID(id string) string {
	trimedName := strings.TrimPrefix(id, varIDPrefix)
	return strings.ToLower(strings.ReplaceAll(trimedName, "_", "-"))
}

func getTailoringID(tp *cmpv1alpha1.TailoredProfile) string {
	return fmt.Sprintf("xccdf_%s_tailoring_%s", XCCDFNamespace, tp.Name)
}

func getSelectElementFromCRRule(rule *cmpv1alpha1.Rule, enable bool) SelectElement {
	return SelectElement{
		IDRef:    rule.ID,
		Selected: enable,
	}
}

func getSelections(tp *cmpv1alpha1.TailoredProfile, rules map[string]*cmpv1alpha1.Rule) []SelectElement {
	selections := []SelectElement{}
	for _, selection := range tp.Spec.EnableRules {
		rule := rules[selection.Name]
		selections = append(selections, getSelectElementFromCRRule(rule, true))
	}

	for _, selection := range tp.Spec.DisableRules {
		rule := rules[selection.Name]
		selections = append(selections, getSelectElementFromCRRule(rule, false))
	}
	return selections
}

func getValuesFromVariables(variables []*cmpv1alpha1.Variable) []SetValueElement {
	values := []SetValueElement{}

	for _, varObj := range variables {
		values = append(values, SetValueElement{
			IDRef: varObj.ID,
			Value: varObj.Value,
		})
	}

	return values
}

// TailoredProfileToXML gets an XML string from a TailoredProfile and the corresponding Profile
func TailoredProfileToXML(tp *cmpv1alpha1.TailoredProfile, p *cmpv1alpha1.Profile, pb *cmpv1alpha1.ProfileBundle, rules map[string]*cmpv1alpha1.Rule, variables []*cmpv1alpha1.Variable) (string, error) {
	tailoring := TailoringElement{
		XMLNamespaceURI: XCCDFURI,
		ID:              getTailoringID(tp),
		Version: VersionElement{
			Time: time.Now().Format(time.RFC3339),
			// TODO(jaosorior): Establish a TailoredProfile versioning mechanism
			Value: "1",
		},
		Benchmark: BenchmarkElement{
			// NOTE(jaosorior): Both this operator and the compliance-operator
			// assume the content will be mounted on a "content/" directory
			Href: filepath.Join("/content", pb.Spec.ContentFile),
		},
		Profile: ProfileElement{
			ID:         GetXCCDFProfileID(tp),
			Extends:    p.ID,
			Selections: getSelections(tp, rules),
			Values:     getValuesFromVariables(variables),
		},
	}
	if tp.Spec.Title != "" {
		tailoring.Profile.Title = &TitleOrDescriptionElement{
			Override: true,
			Value:    tp.Spec.Title,
		}
	}
	if tp.Spec.Description != "" {
		tailoring.Profile.Description = &TitleOrDescriptionElement{
			Override: true,
			Value:    tp.Spec.Description,
		}
	}

	output, err := xml.MarshalIndent(tailoring, "", "  ")
	if err != nil {
		return "", err
	}
	return XMLHeader + "\n" + string(output), nil
}
