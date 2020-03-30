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
		expected map[string]string
		data     string
	}{
		"ssg-ocp4-ds.xml(mocked) new": {
			profile: "xccdf_org.ssgproject.content_profile_platform-moderate",
			data:    "../../tests/data/ssg-ocp4-ds-new.xml",
			expected: map[string]string{
				"/apis/config.openshift.io/v1/oauths/cluster": "/idp.yml",
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
		input            string
		expectedAPIPath  string
		expectedSavePath string
	}{
		"first": {
			input:            `<warning category="general" lang="en-US"><code class="ocp-api-endpoint">/apis/config.openshift.io/v1/oauths/cluster</code><code class="ocp-dump-location"><sub idref="xccdf_org.ssgproject.content_value_ocp_data_root" use="legacy" />/idp.yml</code>file.</warning>`,
			expectedAPIPath:  "/apis/config.openshift.io/v1/oauths/cluster",
			expectedSavePath: "/idp.yml",
		},
	}
	for n, tc := range tests {
		t.Run(n, func(t *testing.T) {
			api, save := getPathsFromWarningXML(tc.input)
			if api != tc.expectedAPIPath {
				t.Errorf("expected API path %s, got %s", tc.expectedAPIPath, api)
			}
			if save != tc.expectedSavePath {
				t.Errorf("expected save path %s, got %s", tc.expectedSavePath, save)
			}
		})
	}
}
