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
			Expect(xmlToMarkdown(validXml)).To(Equal(text))
		})

		It("Should handle empty input", func() {
			Expect(xmlToMarkdown("")).To(Equal(""))
		})

	})

	Context("XML to HTML", func() {
		It("Should parse", func() {
			const (
				html = "System running in FIPS mode is indicated by kernel parameter<code>'crypto.fips_enabled'</code>. This parameter should be set to<code>1</code>in FIPS mode.\nTo enable FIPS mode, run the following command:<p><pre>fips-mode-setup --enable</pre></p>"
			)
			Expect(xmlToHtml(validXml)).To(Equal(html))
		})

		It("Should pass through unknown namespaces", func() {
			Expect(xmlToHtml("kernel parameter<xxx:code>&#39;crypto.fips_enabled&#39;</xxx:code>")).To(Equal("kernel parameter<xxx:code>'crypto.fips_enabled'</xxx:code>"))
		})
	})
})
