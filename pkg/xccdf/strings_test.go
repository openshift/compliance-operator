package xccdf

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Testing parsing XML", func() {

	Context("Descriptions", func() {
		It("renders the description", func() {
			desc := `<xccdf-1.2:description xml:lang="en-US">To configure Audit daemon to issue an explicit flush to disk command
after writing 50 records, set <html:code>freq</html:code> to <html:code>50</html:code>
in <html:code>/etc/audit/auditd.conf</html:code>.</xccdf-1.2:description>`
			expected := `To configure Audit daemon to issue an explicit flush to disk command
after writing 50 records, set <html:code>freq</html:code> to <html:code>50</html:code>
in <html:code>/etc/audit/auditd.conf</html:code>.`
			str, err := GetDescriptionFromXMLString(desc)
			Expect(err).To(BeNil())
			Expect(str).To(Equal(expected))
		})

		It("fails if wrong object given", func() {
			desc := `<xccdf-1.2:warning xml:lang="en-US" category="management">When the <html:code>PodSecurityPolicy</html:code> admission plugin is in use, there
needs to be at least one <html:code>PodSecurityPolicy</html:code> in place for ANY pods to
be admitted.</xccdf-1.2:warning>`
			_, err := GetDescriptionFromXMLString(desc)
			Expect(err).To(Not(BeNil()))
		})
	})

	Context("Rationales", func() {
		It("renders the rationale", func() {
			rat := `<xccdf-1.2:rationale xml:lang="en-US">Using <html:code>EventRateLimit</html:code> admission control enforces a limit on the
number of events that the API Server will accept in a given time slice.
In a large multi-tenant cluster, there might be a small percentage of
misbehaving tenants which could have a significant impact on the
performance of the cluster overall. It is recommended to limit the rate
of events that the API Server will accept.</xccdf-1.2:rationale>`
			expected := `Using <html:code>EventRateLimit</html:code> admission control enforces a limit on the
number of events that the API Server will accept in a given time slice.
In a large multi-tenant cluster, there might be a small percentage of
misbehaving tenants which could have a significant impact on the
performance of the cluster overall. It is recommended to limit the rate
of events that the API Server will accept.`
			str, err := GetRationaleFromXMLString(rat)
			Expect(err).To(BeNil())
			Expect(str).To(Equal(expected))
		})
	})

	Context("Warnings", func() {
		It("renders the warnings", func() {
			warn := `<xccdf-1.2:warning xml:lang="en-US" category="management">When the <html:code>PodSecurityPolicy</html:code> admission plugin is in use, there
needs to be at least one <html:code>PodSecurityPolicy</html:code> in place for ANY pods to
be admitted.</xccdf-1.2:warning>`
			expected := `When the <html:code>PodSecurityPolicy</html:code> admission plugin is in use, there
needs to be at least one <html:code>PodSecurityPolicy</html:code> in place for ANY pods to
be admitted.`
			str, err := GetWarningFromXMLString(warn)
			Expect(err).To(BeNil())
			Expect(str).To(Equal(expected))
		})
	})
})
