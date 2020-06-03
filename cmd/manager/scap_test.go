package main

import (
	"os"
	"reflect"
	"testing"
)

func TestSCAPContentParsing(t *testing.T) {
	debugLog = true
	tests := map[string]struct {
		profile  string
		expected []string
		data     string
	}{
		"ssg-ocp4-ds.xml(mocked) new": {
			profile: "xccdf_org.ssgproject.content_profile_platform-moderate",
			data:    "../../tests/data/ssg-ocp4-ds-new.xml",
			expected: []string{
				"/apis/config.openshift.io/v1/oauths/cluster",
			},
		},
	}
	for n, tc := range tests {
		t.Run(n, func(t *testing.T) {
			f, err := os.Open(tc.data)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			c, err := parseContent(f)
			if err != nil {
				t.Fatal(err)
			}

			got := getResourcePaths(c, tc.profile)
			if !reflect.DeepEqual(tc.expected, got) {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestParseWarning(t *testing.T) {
	debugLog = true
	tests := map[string]struct {
		input           string
		expectedAPIPath string
	}{
		"first": {
			input:           `<warning category="general" lang="en-US"><code class="ocp-api-endpoint">/apis/config.openshift.io/v1/oauths/cluster</code><code class="ocp-dump-location"><sub idref="xccdf_org.ssgproject.content_value_ocp_data_root" use="legacy" />/idp.yml</code>file.</warning>`,
			expectedAPIPath: "/apis/config.openshift.io/v1/oauths/cluster",
		},
	}
	for n, tc := range tests {
		t.Run(n, func(t *testing.T) {
			api := getPathFromWarningXML(tc.input)
			if api != tc.expectedAPIPath {
				t.Errorf("expected API path %s, got %s", tc.expectedAPIPath, api)
			}
		})
	}
}

func TestSaveDirectoryAndFile(t *testing.T) {
	debugLog = true
	tests := map[string]struct {
		root         string
		path         string
		expectedDir  string
		expectedFile string
	}{
		"1": {
			root:         "/tmp",
			path:         "/apis/foo",
			expectedDir:  "/tmp/apis",
			expectedFile: "foo",
		},
		"2": {
			root:         "/",
			path:         "/apis/foo/bar",
			expectedDir:  "/apis/foo",
			expectedFile: "bar",
		},
		"3": {
			root:         "/tmp/foo",
			path:         "/apis/foo/bar/baz",
			expectedDir:  "/tmp/foo/apis/foo/bar",
			expectedFile: "baz",
		},
	}
	for n, tc := range tests {
		t.Run(n, func(t *testing.T) {
			dir, file, err := getSaveDirectoryAndFileName(tc.root, tc.path)
			if err != nil {
				t.Error(err)
			}
			if dir != tc.expectedDir {
				t.Errorf("expected dir %s, got %s", tc.expectedDir, dir)
			}
			if file != tc.expectedFile {
				t.Errorf("expected file %s, got %s", tc.expectedFile, file)
			}
		})
	}
}
