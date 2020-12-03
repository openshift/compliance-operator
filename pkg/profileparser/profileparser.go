package profileparser

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/xccdf"
	"github.com/subchen/go-xmldom"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/apiserver/pkg/storage/names"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	machineConfigFixType = "urn:xccdf:fix:script:ignition"
	kubernetesFixType    = "urn:xccdf:fix:script:kubernetes"

	controlAnnotationBase = "control.compliance.openshift.io/"

	rhacmStdsAnnotationKey   = "policies.open-cluster-management.io/standards"
	rhacmCtrlsAnnotationsKey = "policies.open-cluster-management.io/controls"
)

var log = logf.Log.WithName("profileparser")

type ParserConfig struct {
	DataStreamPath   string
	ProfileBundleKey types.NamespacedName
	Client           runtimeclient.Client
	Scheme           *k8sruntime.Scheme
}

func LogAndReturnError(errormsg string) error {
	log.Info(errormsg)
	return fmt.Errorf(errormsg)
}

func GetPrefixedName(pbName, objName string) string {
	return pbName + "-" + objName
}

func ParseBundle(contentDom *xmldom.Document, pb *cmpv1alpha1.ProfileBundle, pcfg *ParserConfig) error {
	// One go routine per type
	errors := make(chan error)
	done := make(chan string)
	var wg sync.WaitGroup
	wg.Add(3)
	stdParser := newStandardParser()
	nonce := names.SimpleNameGenerator.GenerateName(fmt.Sprintf("pb-%s", pb.Name))
	go func() {
		profErr := ParseProfilesAndDo(contentDom, pb, nonce, func(p *cmpv1alpha1.Profile) error {
			err := parseAction(p, "Profile", pb, pcfg, func(found, updated interface{}) error {
				foundProfile, ok := found.(*cmpv1alpha1.Profile)
				if !ok {
					return fmt.Errorf("unexpected type")
				}
				updatedProfile, ok := updated.(*cmpv1alpha1.Profile)
				if !ok {
					return fmt.Errorf("unexpected type")
				}

				foundProfile.Annotations = updatedProfile.Annotations
				foundProfile.ProfilePayload = *updatedProfile.ProfilePayload.DeepCopy()
				return pcfg.Client.Update(context.TODO(), foundProfile)
			})
			return err
		})

		if profErr != nil {
			errors <- profErr
		}

		if err := deleteObsoleteItems(pcfg.Client, "Profile", pb.Name, pb.Namespace, nonce); err != nil {
			errors <- err
		}
		wg.Done()
	}()

	go func() {
		ruleErr := ParseRulesAndDo(contentDom, stdParser, pb, nonce, func(r *cmpv1alpha1.Rule) error {
			if r.Annotations == nil {
				r.Annotations = make(map[string]string)
			}
			r.Annotations[cmpv1alpha1.RuleIDAnnotationKey] = r.Name

			err := parseAction(r, "Rule", pb, pcfg, func(found, updated interface{}) error {
				foundRule, ok := found.(*cmpv1alpha1.Rule)
				if !ok {
					return fmt.Errorf("unexpected type")
				}
				updatedRule, ok := updated.(*cmpv1alpha1.Rule)
				if !ok {
					return fmt.Errorf("unexpected type")
				}

				foundRule.Annotations = updatedRule.Annotations
				foundRule.RulePayload = *updatedRule.RulePayload.DeepCopy()
				return pcfg.Client.Update(context.TODO(), foundRule)
			})
			return err
		})

		if ruleErr != nil {
			errors <- ruleErr
		}

		if err := deleteObsoleteItems(pcfg.Client, "Rule", pb.Name, pb.Namespace, nonce); err != nil {
			errors <- err
		}
		wg.Done()
	}()

	go func() {
		varErr := ParseVariablesAndDo(contentDom, pb, nonce, func(v *cmpv1alpha1.Variable) error {
			err := parseAction(v, "Variable", pb, pcfg, func(found, updated interface{}) error {
				foundVariable, ok := found.(*cmpv1alpha1.Variable)
				if !ok {
					return fmt.Errorf("unexpected type")
				}
				updatedVariable, ok := updated.(*cmpv1alpha1.Variable)
				if !ok {
					return fmt.Errorf("unexpected type")
				}

				foundVariable.Annotations = updatedVariable.Annotations
				foundVariable.VariablePayload = *updatedVariable.VariablePayload.DeepCopy()
				return pcfg.Client.Update(context.TODO(), foundVariable)
			})
			return err
		})

		if varErr != nil {
			errors <- varErr
		}

		if err := deleteObsoleteItems(pcfg.Client, "Variable", pb.Name, pb.Namespace, nonce); err != nil {
			errors <- err
		}
		wg.Done()
	}()

	// Wait for the go routines to end and close the "done" channel
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// carry on
		break
	case err := <-errors:
		close(errors)
		return err
	}

	return nil
}

type parsedItemIface interface {
	metav1.Object
	k8sruntime.Object
}

func parseAction(parsedItem parsedItemIface, kind string, pb *cmpv1alpha1.ProfileBundle, pcfg *ParserConfig, updateFn func(found, updated interface{}) error) error {
	// overwrite name
	itemName := parsedItem.GetName()
	parsedItem.SetName(GetPrefixedName(pb.Name, itemName))

	labels := parsedItem.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[cmpv1alpha1.ProfileBundleOwnerLabel] = pb.Name
	parsedItem.SetLabels(labels)

	if err := controllerutil.SetControllerReference(pb, parsedItem, pcfg.Scheme); err != nil {
		return err
	}

	key := types.NamespacedName{Name: parsedItem.GetName(), Namespace: parsedItem.GetNamespace()}
	if err := createOrUpdate(pcfg.Client, kind, key, parsedItem, updateFn); err != nil {
		return err
	}

	return nil
}

func createOrUpdate(cli runtimeclient.Client, kind string, key types.NamespacedName, obj k8sruntime.Object, updateFn func(found, updated interface{}) error) error {
	log.Info("Creating object", "kind", kind, "key", key)
	found := obj.DeepCopyObject()
	err := cli.Get(context.TODO(), key, found)
	if errors.IsNotFound(err) {
		err := cli.Create(context.TODO(), obj)
		if err != nil {
			log.Error(err, "Failed to create object", "kind", kind, "key", key)
			return err
		}
		return nil
	} else if err != nil {
		return err
	}

	// Object exist, call up to update
	if err := updateFn(found, obj); err != nil {
		return err
	}

	return nil
}

func deleteObsoleteItems(cli runtimeclient.Client, kind string, pbName, namespace string, nonce string) error {
	list := unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   cmpv1alpha1.SchemeGroupVersion.Group,
		Version: cmpv1alpha1.SchemeGroupVersion.Version,
		Kind:    kind + "List",
	})
	inNs := runtimeclient.InNamespace(namespace)
	withPbOwnerLabel := runtimeclient.MatchingLabels{
		cmpv1alpha1.ProfileBundleOwnerLabel: pbName,
	}

	log.Info("Checking for unused object", "kind", kind, "owner", pbName, "namespace", namespace)
	if err := cli.List(context.TODO(), &list, inNs, withPbOwnerLabel); err != nil {
		return err
	}

	// TODO: Using the annotations forces us to iterate over all objects of
	// a type. This might be inefficient with a large number of objects,
	// if this ever becomes a performance problem, use labels instead
	// with a short version of the hash
	for _, item := range list.Items {
		err := deleteIfNotCurrentDigest(cli, nonce, &item)
		if err != nil {
			return err
		}
	}

	return nil
}

func deleteIfNotCurrentDigest(client runtimeclient.Client, imageDigest string, item *unstructured.Unstructured) error {
	itemDigest := item.GetAnnotations()[cmpv1alpha1.ProfileImageDigestAnnotation]
	if itemDigest == imageDigest {
		return nil
	}

	log.Info("Deleting object no longer used by the current profileBundle", "kind", item.GetKind(), "name", item.GetName())
	return client.Delete(context.TODO(), item)
}

func getVariableType(varNode *xmldom.Node) cmpv1alpha1.VariableType {
	typeAttr := varNode.GetAttribute("type")
	if typeAttr == nil {
		return cmpv1alpha1.VarTypeString
	}

	switch typeAttr.Value {
	case "string":
		return cmpv1alpha1.VarTypeString
	case "number":
		return cmpv1alpha1.VarTypeNumber
	case "boolean":
		return cmpv1alpha1.VarTypeBool
	}

	return cmpv1alpha1.VarTypeString
}

func ParseProfilesAndDo(contentDom *xmldom.Document, pb *cmpv1alpha1.ProfileBundle, nonce string, action func(p *cmpv1alpha1.Profile) error) error {
	benchmarks := contentDom.Root.Query("//Benchmark")
	for _, bench := range benchmarks {
		productType, productName := getProductTypeAndName(bench, cmpv1alpha1.ScanTypeNode, "")
		if err := parseProfileFromNode(bench, pb, productType, productName, nonce, action); err != nil {
			return err
		}
	}

	return nil
}

func parseProfileFromNode(profileRoot *xmldom.Node, pb *cmpv1alpha1.ProfileBundle, defType cmpv1alpha1.ComplianceScanType, defName, nonce string, action func(p *cmpv1alpha1.Profile) error) error {
	profileObjs := profileRoot.Query("//Profile")
	for _, profileObj := range profileObjs {

		id := profileObj.GetAttributeValue("id")
		if id == "" {
			return LogAndReturnError("no id in profile")
		}
		title := profileObj.FindOneByName("title")
		if title == nil {
			return LogAndReturnError("no title in profile")
		}
		description := profileObj.FindOneByName("description")
		if description == nil {
			return LogAndReturnError("no description in profile")
		}
		log.Info("Found profile", "id", id)

		// In case the profile sets its own CPE string
		productType, productName := getProductTypeAndName(profileObj, defType, defName)
		log.Info("Platform info", "type", productType, "name", productName)

		ruleObjs := profileObj.FindByName("select")
		selectedrules := []cmpv1alpha1.ProfileRule{}
		for _, ruleObj := range ruleObjs {
			idref := ruleObj.GetAttributeValue("idref")
			if idref == "" {
				log.Info("no idref in rule")
				continue
			}
			selected := ruleObj.GetAttributeValue("selected")
			if selected == "true" {
				ruleName := GetPrefixedName(pb.Name, xccdf.GetRuleNameFromID(idref))
				selectedrules = append(selectedrules, cmpv1alpha1.NewProfileRule(ruleName))
			}
		}

		selectedvalues := []cmpv1alpha1.ProfileValue{}
		valueObjs := profileObj.FindByName("set-value")
		for _, valueObj := range valueObjs {
			idref := valueObj.GetAttributeValue("idref")
			if idref == "" {
				log.Info("no idref in rule")
				continue
			}
			selectedvalues = append(selectedvalues, cmpv1alpha1.ProfileValue(idref))
		}

		p := cmpv1alpha1.Profile{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Profile",
				APIVersion: cmpv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      xccdf.GetProfileNameFromID(id),
				Namespace: pb.Namespace,
				Annotations: map[string]string{
					cmpv1alpha1.ProductAnnotation:     productName,
					cmpv1alpha1.ProductTypeAnnotation: string(productType),
				},
			},
			ProfilePayload: cmpv1alpha1.ProfilePayload{
				ID:          id,
				Title:       title.Text,
				Description: description.Text,
				Rules:       selectedrules,
				Values:      selectedvalues,
			},
		}

		annotateWithNonce(&p, nonce)

		err := action(&p)
		if err != nil {
			log.Error(err, "couldn't execute action")
			return err
		}
	}

	return nil
}

func getProductTypeAndName(root *xmldom.Node, defaultType cmpv1alpha1.ComplianceScanType, defaultName string) (cmpv1alpha1.ComplianceScanType, string) {
	// Loop ourselves b/c we are only interested in direct children
	for _, child := range root.Children {
		if child.Name == "platform" {
			return parseProductTypeAndName(child.GetAttributeValue("idref"), defaultType, defaultName)
		}
	}

	return defaultType, defaultName
}

func parseProductTypeAndName(idref string, defaultType cmpv1alpha1.ComplianceScanType, defaultName string) (cmpv1alpha1.ComplianceScanType, string) {
	const partIdx = 0
	const cpePrefix = "cpe:/"

	var productType cmpv1alpha1.ComplianceScanType
	log.Info("Parsing CPE", "cpe ID", idref)

	// example: cpe:/a:redhat:enterprise_linux_coreos:4
	if strings.HasPrefix(idref, "#") || !strings.HasPrefix(idref, cpePrefix) {
		log.Info("references are not supported or an unsupported format")
		return defaultType, defaultName
	}

	// Now we know it begins with cpePrefix
	idref = strings.TrimPrefix(idref, cpePrefix)
	cpePieces := strings.Split(idref, ":")
	if len(cpePieces) == 0 || (len(cpePieces) == 1 && cpePieces[0] == idref) {
		log.Info("The CPE ID is too short")
		return defaultType, defaultName
	}
	log.Info("exploded CPE", "cpePieces", cpePieces)

	log.Info("CPE part", "part", cpePieces[partIdx])
	switch cpePieces[partIdx] {
	case "o":
		productType = cmpv1alpha1.ScanTypeNode
	default:
		// We assume anything we don't know is a platform...
		productType = cmpv1alpha1.ScanTypePlatform
	}

	var productName string
	if len(cpePieces) > 2 {
		productName = strings.Join(cpePieces[1:], "_")
	}
	return productType, productName
}

func ParseVariablesAndDo(contentDom *xmldom.Document, pb *cmpv1alpha1.ProfileBundle, nonce string, action func(v *cmpv1alpha1.Variable) error) error {
	varObjs := contentDom.Root.Query("//Value")
	for _, varObj := range varObjs {
		hidden := varObj.GetAttributeValue("hidden")
		if hidden == "true" {
			// this is typically used for functions
			continue
		}

		id := varObj.GetAttributeValue("id")
		log.Info("Found variable", "id", id)

		if id == "" {
			return LogAndReturnError("no id in variable")
		}
		title := varObj.FindOneByName("title")
		if title == nil {
			return LogAndReturnError("no title in variable")
		}

		v := cmpv1alpha1.Variable{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Variable",
				APIVersion: cmpv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      xccdf.GetVariableNameFromID(id),
				Namespace: pb.Namespace,
			},
			VariablePayload: cmpv1alpha1.VariablePayload{
				ID:    id,
				Title: title.Text,
			},
		}

		description := varObj.FindOneByName("description")
		if description != nil {
			desc, err := xccdf.GetDescriptionFromXMLString(description.XML())
			if err != nil {
				log.Error(err, "couldn't parse a rule's description")
				desc = ""
			}
			v.Description = desc
		}

		v.Type = getVariableType(varObj)

		// extract the value and optionally the allowed value list
		err := parseVarValues(varObj, &v)
		if err != nil {
			log.Error(err, "couldn't set variable value")
			// We continue even if there's an error.
			continue
		}

		annotateWithNonce(&v, nonce)

		err = action(&v)
		if err != nil {
			log.Error(err, "couldn't execute action for variable")
			// We continue even if there's an error.
			continue
		}
	}
	return nil
}

func parseVarValues(varNode *xmldom.Node, v *cmpv1alpha1.Variable) error {
	for _, val := range varNode.FindByName("value") {
		selector := val.GetAttribute("selector")
		if selector != nil {
			// this is an enum choice
			v.Selections = append(v.Selections, cmpv1alpha1.ValueSelection{
				Description: selector.Value,
				Value:       val.Text,
			})
			continue
		}

		// this is the default value
		if v.Value != "" {
			return fmt.Errorf("attempting to set multiple values for variable %s; already had %s", v.ID, v.Value)
		}
		v.Value = val.Text
	}

	return nil
}

func ParseRulesAndDo(contentDom *xmldom.Document, stdParser *referenceParser, pb *cmpv1alpha1.ProfileBundle, nonce string, action func(p *cmpv1alpha1.Rule) error) error {
	ruleObjs := contentDom.Root.Query("//Rule")
	for _, ruleObj := range ruleObjs {
		id := ruleObj.GetAttributeValue("id")
		if id == "" {
			return LogAndReturnError("no id in rule")
		}
		title := ruleObj.FindOneByName("title")
		if title == nil {
			return LogAndReturnError("no title in rule")
		}
		log.Info("Found rule", "id", id)

		description := ruleObj.FindOneByName("description")
		rationale := ruleObj.FindOneByName("rationale")
		warning := ruleObj.FindOneByName("warning")
		severity := ruleObj.GetAttributeValue("severity")

		fixes := []cmpv1alpha1.FixDefinition{}
		foundPlatformMap := make(map[string]bool)
		fixNodeObjs := ruleObj.FindByName("fix")
		for _, fixNodeObj := range fixNodeObjs {
			if !isRelevantFix(fixNodeObj) {
				continue
			}
			platform := fixNodeObj.GetAttributeValue("platform")
			if foundPlatformMap[platform] {
				// We already have a remediation for this platform
				continue
			}

			rawFixReader := strings.NewReader(fixNodeObj.Text)
			fixKubeObj, err := readObjFromYAML(rawFixReader)
			if err != nil {
				log.Info("Couldn't parse Kubernetes object from fix")
				continue
			}

			disruption := fixNodeObj.GetAttributeValue("disruption")

			newFix := cmpv1alpha1.FixDefinition{
				Disruption: disruption,
				Platform:   platform,
				FixObject:  fixKubeObj,
			}
			fixes = append(fixes, newFix)
			foundPlatformMap[platform] = true
		}

		// note: stdParser is a global variable initialized in init()
		annotations, err := stdParser.parseXmlNode(ruleObj)
		if err != nil {
			log.Error(err, "couldn't annotate a rule")
			// We continue even if there's an error.
		}

		p := cmpv1alpha1.Rule{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Rule",
				APIVersion: cmpv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        xccdf.GetRuleNameFromID(id),
				Namespace:   pb.Namespace,
				Annotations: annotations,
			},
			RulePayload: cmpv1alpha1.RulePayload{
				ID:             id,
				Title:          title.Text,
				AvailableFixes: nil,
			},
		}
		if description != nil {
			desc, err := xccdf.GetDescriptionFromXMLString(description.XML())
			if err != nil {
				log.Error(err, "couldn't parse a rule's description")
				desc = ""
			}
			p.Description = desc
		}
		if rationale != nil {
			rat, err := xccdf.GetRationaleFromXMLString(rationale.XML())
			if err != nil {
				log.Error(err, "couldn't parse a rule's rationale")
				rat = ""
			}
			p.Rationale = rat
		}
		if warning != nil {
			warn, err := xccdf.GetWarningFromXMLString(warning.XML())
			if err != nil {
				log.Error(err, "couldn't parse a rule's warning")
				warn = ""
			}
			p.Warning = warn
		}
		if severity != "" {
			p.Severity = severity
		}
		if len(fixes) > 0 {
			p.AvailableFixes = fixes
		}

		annotateWithNonce(&p, nonce)

		err = action(&p)
		if err != nil {
			log.Error(err, "couldn't execute action for rule")
			// We continue even if there's an error.
		}
	}

	return nil
}

// Reads a YAML file and returns an unstructured object from it. This object
// can be taken into use by the dynamic client
func readObjFromYAML(r io.Reader) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	dec := k8syaml.NewYAMLToJSONDecoder(r)
	err := dec.Decode(obj)
	return obj, err
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

func annotateWithNonce(o metav1.Object, nonce string) {
	annotations := o.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[cmpv1alpha1.ProfileImageDigestAnnotation] = nonce
	o.SetAnnotations(annotations)
}

type complianceStandard struct {
	Name        string
	hrefMatcher *regexp.Regexp
}

type annotationsFormatterFn func(annotations map[string]string, std, ctrl string)

type referenceParser struct {
	registeredStds       []*complianceStandard
	annotationFormatters []annotationsFormatterFn
}

func newStandardParser() *referenceParser {
	p := referenceParser{}
	p.registeredStds = make([]*complianceStandard, 0)
	p.annotationFormatters = make([]annotationsFormatterFn, 0)
	err := p.registerStandard("NIST-800-53", `^http://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST\.SP\.800-53r4\.pdf$`)
	if err != nil {
		log.Error(err, "Could not register NIST-800-53 reference parser") // not much we can do here..
	}

	p.registerFormatter(profileOperatorFormatter)
	p.registerFormatter(rhacmFormatter)
	return &p
}

func (p *referenceParser) registerStandard(name, hrefRegexp string) error {
	var err error

	newStd := complianceStandard{
		Name: name,
	}

	if newStd.hrefMatcher, err = regexp.Compile(hrefRegexp); err != nil {
		return err
	}

	p.registeredStds = append(p.registeredStds, &newStd)
	return nil
}

func (p *referenceParser) registerFormatter(formatter annotationsFormatterFn) {
	p.annotationFormatters = append(p.annotationFormatters, formatter)
}

func (p *referenceParser) parseXmlNode(ruleObj *xmldom.Node) (map[string]string, error) {
	ruleAnnotations := make(map[string]string)

	for _, refEl := range ruleObj.FindByName("reference") {
		href := refEl.GetAttributeValue("href")
		if href == "" {
			continue
		}

		for _, std := range p.registeredStds {
			if !std.hrefMatcher.MatchString(href) {
				continue
			}

			for _, formatter := range p.annotationFormatters {
				formatter(ruleAnnotations, std.Name, refEl.Text)
			}
		}
	}

	return ruleAnnotations, nil
}

func profileOperatorFormatter(annotations map[string]string, std, ctrl string) {
	const poSep = ";"
	key := controlAnnotationBase + std

	appendKeyWithSep(annotations, key, ctrl, poSep)
}

func rhacmFormatter(annotations map[string]string, std, ctrl string) {
	const rhacmSeperator = ","

	appendKeyWithSep(annotations, rhacmStdsAnnotationKey, std, rhacmSeperator)
	appendKeyWithSep(annotations, rhacmCtrlsAnnotationsKey, ctrl, rhacmSeperator)
}

func appendKeyWithSep(annotations map[string]string, key, item, sep string) {
	curVal, ok := annotations[key]
	if !ok {
		annotations[key] = item
		return
	}

	curList := strings.Split(curVal, sep)
	for _, k := range curList {
		if k == item {
			return
		}
	}
	annotations[key] = strings.Join(append(curList, item), sep)
}
