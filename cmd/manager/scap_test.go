package main

import (
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"

	"github.com/antchfx/xmlquery"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/compliance-operator/pkg/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("Testing SCAP parsing and storage", func() {
	// Turn to `true` if debugging
	debugLog = true

	Context("Parsing SCAP Content", func() {
		var dataStreamFile *os.File
		var contentDS *xmlquery.Node

		BeforeEach(func() {
			var err error
			dataStreamFile, err = os.Open("../../tests/data/ssg-ocp4-ds-new.xml")
			Expect(err).To(BeNil())
		})
		AfterEach(func() {
			dataStreamFile.Close()
		})

		It("Gets the appropriate resource URIs", func() {
			By("parsing content without errors")
			var err error
			contentDS, err = parseContent(dataStreamFile)
			Expect(err).To(BeNil())

			By("parsing content for warnings")
			expected := []utils.ResourcePath{
				{
					ObjPath:  "/apis/config.openshift.io/v1/oauths/cluster",
					DumpPath: "/apis/config.openshift.io/v1/oauths/cluster",
				},
				{
					ObjPath:  "/api/v1/namespaces/openshift-kube-apiserver/configmaps/config",
					DumpPath: "/api/v1/namespaces/openshift-kube-apiserver/configmaps/config",
				},
			}
			got := getResourcePaths(contentDS, contentDS, "xccdf_org.ssgproject.content_profile_platform-moderate")
			Expect(got).To(Equal(expected))
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

var _ = Describe("Testing filtering", func() {
	Context("Filtering namespaces", func() {
		var rawns []byte
		BeforeEach(func() {
			nsFile, err := os.Open("../../tests/data/namespaces.json")
			Expect(err).To(BeNil())
			var readErr error
			rawns, readErr = ioutil.ReadAll(nsFile)
			Expect(readErr).To(BeNil())
		})
		It("filters namespaces appropriately", func() {
			filteredOut, filterErr := filter(context.TODO(), rawns,
				`[.items[] | select((.metadata.name | startswith("openshift") | not) and (.metadata.name | startswith("kube-") | not) and .metadata.name != "default")]`)
			Expect(filterErr).To(BeNil())
			nsArr := []interface{}{}
			unmErr := json.Unmarshal(filteredOut, &nsArr)
			Expect(unmErr).To(BeNil())
			Expect(nsArr).To(HaveLen(2))
		})
	})

	Context("Testing errors", func() {
		It("outputs error if it can't create filter", func() {
			_, filterErr := filter(context.TODO(), []byte{},
				`.items[`)
			Expect(filterErr).ToNot(BeNil())
		})
		Context("Filtering namespaces", func() {
			var rawns []byte
			BeforeEach(func() {
				nsFile, err := os.Open("../../tests/data/namespaces.json")
				Expect(err).To(BeNil())
				var readErr error
				rawns, readErr = ioutil.ReadAll(nsFile)
				Expect(readErr).To(BeNil())
			})

			It("skips extra results", func() {
				_, filterErr := filter(context.TODO(), rawns, `.items[]`)
				Expect(filterErr).Should(MatchError(MoreThanOneObjErr))
			})
		})
	})
})

var _ = Describe("Testing fetching", func() {
	Context("fetches", func() {
		It("fetches and stores 404s", func() {
			files, warnings, err := fetch(func(_ string) (io.ReadCloser, error) {
				return nil, errors.NewNotFound(schema.GroupResource{
					Group:    "some group",
					Resource: "some resource",
				}, "some name")
			}, []utils.ResourcePath{{DumpPath: "key"}})

			Expect(err).To(BeNil())
			Expect(files).To(HaveLen(1))
			Expect(string(files["key"])).To(Equal("# kube-api-error=NotFound"))
			Expect(warnings).To(HaveLen(1))
			Expect(warnings[0]).To(Equal("could not fetch : some resource.some group \"some name\" not found"))
		})
	})
})
