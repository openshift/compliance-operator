package main

import (
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/openshift/compliance-operator/pkg/utils"
)

var _ = Describe("Testing SCAP parsing and storage", func() {
	// Turn to `true` if debugging
	debugLog = false

	Context("Parsing SCAP Content", func() {
		var dataStreamFile *os.File
		var contentDS *utils.XMLDocument

		BeforeEach(func() {
			var err error
			dataStreamFile, err = os.Open("../../tests/data/ssg-ocp4-ds-new.xml")
			Expect(err).To(BeNil())
		})
		AfterEach(func() {
			dataStreamFile.Close()
		})

		It("Parses content without errors", func() {
			var err error
			contentDS, err = parseContent(dataStreamFile)
			Expect(err).To(BeNil())
		})

		It("Gets the appropriate resource URIs", func() {
			expected := []string{"/apis/config.openshift.io/v1/oauths/cluster"}
			got := getResourcePaths(contentDS, contentDS, "xccdf_org.ssgproject.content_profile_platform-moderate")
			Expect(got).To(Equal(expected))
		})
	})

	Context("Parsing warnings", func() {
		It("Gets an appropriate path from the datastream", func() {
			expectedAPIPath := "/apis/config.openshift.io/v1/oauths/cluster"

			warning := `<warning category="general" lang="en-US"><code class="ocp-api-endpoint">/apis/config.openshift.io/v1/oauths/cluster</code><code class="ocp-dump-location"><sub idref="xccdf_org.ssgproject.content_value_ocp_data_root" use="legacy" />/idp.yml</code>file.</warning>`
			api := getPathFromWarningXML(warning)
			Expect(api).To(Equal(expectedAPIPath))
		})
	})

	Context("Parses the save path appropriately", func() {
		It("Parses correctly with the root being '/tmp'", func() {
			root := "/tmp"
			path := "/apis/foo"
			expectedDir := "/tmp/apis"
			expectedFile := "foo"

			dir, file, err := getSaveDirectoryAndFileName(root, path)
			Expect(err).To(BeNil())
			Expect(dir).To(Equal(expectedDir))
			Expect(file).To(Equal(expectedFile))
		})

		It("Parses correctly with the root being '/'", func() {
			root := "/"
			path := "/apis/foo/bar"
			expectedDir := "/apis/foo"
			expectedFile := "bar"

			dir, file, err := getSaveDirectoryAndFileName(root, path)
			Expect(err).To(BeNil())
			Expect(dir).To(Equal(expectedDir))
			Expect(file).To(Equal(expectedFile))
		})

		It("Parses correctly with the root being '/tmp/foo'", func() {
			root := "/tmp/foo"
			path := "/apis/foo/bar/baz"
			expectedDir := "/tmp/foo/apis/foo/bar"
			expectedFile := "baz"

			dir, file, err := getSaveDirectoryAndFileName(root, path)
			Expect(err).To(BeNil())
			Expect(dir).To(Equal(expectedDir))
			Expect(file).To(Equal(expectedFile))
		})
	})
})
