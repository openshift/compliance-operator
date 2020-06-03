package xccdf

import "encoding/xml"

// GetDescriptionFromXMLString gets a description from the given XML string
func GetDescriptionFromXMLString(raw string) (string, error) {
	type Description struct {
		XMLName xml.Name `xml:"description"`
		Lang    string   `xml:"lang,attr,omitempty"`
		Value   string   `xml:",innerxml"`
	}
	obj := &Description{}
	err := xml.Unmarshal([]byte(raw), obj)
	return obj.Value, err
}

// GetRationaleFromXMLString gets the rationale from the given XML string
func GetRationaleFromXMLString(raw string) (string, error) {
	type Rationale struct {
		XMLName xml.Name `xml:"rationale"`
		Lang    string   `xml:"lang,attr,omitempty"`
		Value   string   `xml:",innerxml"`
	}
	obj := &Rationale{}
	err := xml.Unmarshal([]byte(raw), obj)
	return obj.Value, err
}

// GetWarningFromXMLString gets a warning from the given XML string
func GetWarningFromXMLString(raw string) (string, error) {
	type Warning struct {
		XMLName xml.Name `xml:"warning"`
		Lang    string   `xml:"lang,attr,omitempty"`
		Value   string   `xml:",innerxml"`
	}
	obj := &Warning{}
	err := xml.Unmarshal([]byte(raw), obj)
	return obj.Value, err
}
