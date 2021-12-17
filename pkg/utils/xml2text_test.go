package utils

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("XML conversions", func() {
	const (
		validXml = `System running in FIPS mode is indicated by kernel parameter<html:code>&#39;crypto.fips_enabled&#39;</html:code>. This parameter should be set to<html:code>1</html:code>in FIPS mode.&#xA;To enable FIPS mode, run the following command:<html:pre>fips-mode-setup --enable</html:pre>`
	)

	Context("XML to markdown", func() {
		It("Should parse", func() {
			const (
				text = "System running in FIPS mode is indicated by kernel parameter 'crypto.fips_enabled'. This parameter should be set to 1 in FIPS mode. To enable FIPS mode, run the following command:\n\nfips-mode-setup --enable"
			)
			Expect(xmlToMarkdown(validXml, false, false)).To(Equal(text))
		})

		It("Should handle empty input", func() {
			Expect(xmlToMarkdown("", false, false)).To(Equal(""))
		})

	})

	Context("XML to HTML", func() {
		It("Should parse", func() {
			const (
				html = "System running in FIPS mode is indicated by kernel parameter<code>'crypto.fips_enabled'</code>. This parameter should be set to<code>1</code>in FIPS mode.\nTo enable FIPS mode, run the following command:<p><pre>fips-mode-setup --enable</pre></p>"
			)
			Expect(xmlToHtml(validXml, false, false)).To(Equal(html))
		})

		It("Should pass through unknown namespaces", func() {
			Expect(xmlToHtml("kernel parameter<xxx:code>&#39;crypto.fips_enabled&#39;</xxx:code>", false, false)).To(Equal("kernel parameter<xxx:code>'crypto.fips_enabled'</xxx:code>"))
		})
	})

	Context("XML to Markdown render variable", func() {
		const (
			html                 = `SELINUXTYPE=<xccdf-1.2:sub idref="xccdf_org.ssgproject.content_value_var_selinux_policy_name" use="legacy"/>Other.`
			expectedHtml         = `SELINUXTYPE= {{.var_selinux_policy_name}} Other.`
			expectedHtmlRendered = `SELINUXTYPE= 3711 Other.`
		)
		valueList := map[string]string{
			"var_selinux_policy_name":   "3711",
			"var_selinux_policy_name-2": "2138",
			"something":                 "1908",
		}
		preRender := xmlToMarkdown(html, true, true)
		It("Should parse", func() {
			Expect(preRender).To(Equal(expectedHtml))
		})
		renderedText, valueParsed, err := RenderValues(preRender, valueList)
		It("Should render variable without errors", func() {
			Expect(renderedText).To(Equal(expectedHtmlRendered))
			Expect(err).To(BeNil())
			Expect(valueParsed).To(ContainElement("var_selinux_policy_name"))
		})

	})

})
