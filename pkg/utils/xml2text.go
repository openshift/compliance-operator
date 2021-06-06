package utils

import (
	"encoding/xml"
	"github.com/antchfx/xmlquery"
	"github.com/jaytaylor/html2text"
	"io"
	"strings"
)

func XmlNodeAsMarkdown(node *xmlquery.Node) string {
	return xmlToMarkdown(node.OutputXML(false))
}

func xmlToMarkdown(in string) string {
	text, err := html2text.FromString(xmlToHtml(in), html2text.Options{PrettyTables: true, OmitLinks: false})
	if err != nil {
		return in
	}
	return text
}

func xmlToHtml(in string) string {
	builder := strings.Builder{}
	decoder := xml.NewDecoder(strings.NewReader(in))
	for {
		// Read tokens from the XML document in a stream.
		t, err := decoder.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			// ignore errors and try to format as much as possible
			continue
		}
		switch tok := t.(type) {
		case xml.StartElement:
			builder.WriteString(formatElement(tok.Name, "<"))
		case xml.EndElement:
			builder.WriteString(formatElement(tok.Name, "</"))
		case xml.CharData:
			builder.Write(tok)
		}
	}

	return builder.String()
}

func formatElement(elName xml.Name, tag string) string {
	// just pass non-html tags through
	var t string
	if elName.Space != "html" {
		t = tag + elName.Space + ":" + elName.Local + ">"
	} else {
		// enclose pre in a paragraph to force a line break
		if elName.Local == "pre" && tag == "<" {
			t = tag + "p>"
		}

		t += tag + elName.Local + ">"

		if elName.Local == "pre" && tag == "</" {
			t += tag + "p>"
		}
	}
	return t
}
