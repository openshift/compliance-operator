/*
Copyright © 2020 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"

	"github.com/ghodss/yaml"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/compliance-operator/pkg/utils"
)

const (
	contentFileTimeout = 3600
)

// For OpenSCAP content as an XML data stream. Implements ResourceFetcher.
type scapContentDataStream struct {
	// Client for Gets
	client *kubernetes.Clientset
	// Staging objects
	dataStream *xmlquery.Node
	tailoring  *xmlquery.Node
	resources  []string
	found      map[string][]byte
}

func NewDataStreamResourceFetcher(client *kubernetes.Clientset) ResourceFetcher {
	return &scapContentDataStream{
		client: client,
	}
}

func (c *scapContentDataStream) LoadSource(path string) error {
	xml, err := c.loadContent(path)
	if err != nil {
		return err
	}
	c.dataStream = xml
	return nil
}

func (c *scapContentDataStream) LoadTailoring(path string) error {
	xml, err := c.loadContent(path)
	if err != nil {
		return err
	}
	c.tailoring = xml
	return nil
}

func (c *scapContentDataStream) loadContent(path string) (*xmlquery.Node, error) {
	f, err := openNonEmptyFile(path)
	if err != nil {
		return nil, err
	}
	// #nosec
	defer f.Close()
	return parseContent(f)
}

func parseContent(f *os.File) (*xmlquery.Node, error) {
	return utils.ParseContent(bufio.NewReader(f))
}

// Returns the file, but only after it has been created by the other init container.
// This avoids a race.
func openNonEmptyFile(filename string) (*os.File, error) {
	readFileTimeoutChan := make(chan *os.File, 1)

	// gosec complains that the file is passed through an evironment variable. But
	// this is not a security issue because none of the files are user-provided
	cleanFileName := filepath.Clean(filename)

	go func() {
		for {
			// Note that we're cleaning the filename path above.
			// #nosec
			file, err := os.Open(cleanFileName)
			if err == nil {
				fileinfo, err := file.Stat()
				// Only try to use the file if it already has contents.
				if err == nil && fileinfo.Size() > 0 {
					readFileTimeoutChan <- file
				}
			} else if !os.IsNotExist(err) {
				fmt.Println(err)
				os.Exit(1)
			}
			time.Sleep(1 * time.Second)
		}
	}()

	select {
	case file := <-readFileTimeoutChan:
		fmt.Printf("File '%s' found, using.\n", filename)
		return file, nil
	case <-time.After(time.Duration(contentFileTimeout) * time.Second):
		fmt.Println("Timeout. Aborting.")
		os.Exit(1)
	}

	// We shouldn't get here.
	return nil, nil
}

func (c *scapContentDataStream) FigureResources(profile string) error {
	// Always stage the clusteroperators/openshift-apiserver object for version detection.
	found := []string{"/apis/config.openshift.io/v1/clusteroperators/openshift-apiserver"}
	effectiveProfile := profile

	if c.tailoring != nil {
		selected := getResourcePaths(c.tailoring, c.dataStream, profile)
		if len(selected) == 0 {
			fmt.Printf("no valid checks found in tailoring\n")
		}
		found = append(found, selected...)
		// Overwrite profile so the next search uses the extended profile
		effectiveProfile = c.getExtendedProfileFromTailoring(c.tailoring, profile)
		// No profile is being extended
		if effectiveProfile == "" {
			c.resources = found
			return nil
		}
	}

	selected := getResourcePaths(c.dataStream, c.dataStream, effectiveProfile)
	if len(selected) == 0 {
		fmt.Printf("no valid checks found in profile\n")
	}
	found = append(found, selected...)
	c.resources = found
	return nil
}

// getPathsFromRuleWarning finds the API endpoint from in. The expected structure is:
//
//  <warning category="general" lang="en-US"><code class="ocp-api-endpoint">/apis/config.openshift.io/v1/oauths/cluster
//  </code></warning>
func getPathFromWarningXML(in *xmlquery.Node) string {
	DBG("Parsing warning %s", in.OutputXML(false))
	return utils.GetPathFromWarningXML(in)
}

// Collect the resource paths for objects that this scan needs to obtain.
// The profile will have a series of "selected" checks that we grab all of the path info from.
func getResourcePaths(profileDefs *xmlquery.Node, ruleDefs *xmlquery.Node, profile string) []string {
	out := []string{}
	selectedChecks := []string{}

	// First we find the Profile node, to locate the enabled checks.
	DBG("Using profile %s", profile)
	nodes := profileDefs.SelectElements("//xccdf-1.2:Profile")
	if len(nodes) == 0 {
		DBG("no profiles found in datastream")
	}
	for _, node := range nodes {
		profileID := node.SelectAttr("id")
		if profileID != profile {
			continue
		}

		checks := node.SelectElements("//xccdf-1.2:select")
		for _, check := range checks {
			if check.SelectAttr("selected") != "true" {
				continue
			}

			if idRef := check.SelectAttr("idref"); idRef != "" {
				DBG("selected: %v", idRef)
				selectedChecks = append(selectedChecks, idRef)
			}
		}
	}

	checkDefinitions := ruleDefs.SelectElements("//xccdf-1.2:Rule")
	if len(checkDefinitions) == 0 {
		DBG("WARNING: No rules to query (invalid datastream)")
		return out
	}

	// For each of our selected checks, collect the required path info.
	for _, checkID := range selectedChecks {
		var found *xmlquery.Node
		for _, rule := range checkDefinitions {
			if rule.SelectAttr("id") == checkID {
				found = rule
				break
			}
		}
		if found == nil {
			DBG("WARNING: Couldn't find a check for id %s", checkID)
			continue
		}

		// This node is called "warning" and contains the path info. It's not an actual "warning" for us here.
		var warningFound bool
		warningObjs := found.SelectElements("//xccdf-1.2:warning")

		for _, warn := range warningObjs {
			if warn == nil {
				continue
			}
			apiPath := getPathFromWarningXML(warn)
			if len(apiPath) == 0 {
				continue
			}
			// We only care for the first occurrence that works
			out = append(out, apiPath)
			warningFound = true
			break
		}

		if !warningFound {
			DBG("Couldn't find 'warning' child of check %s", checkID)
			continue
		}

	}

	return out
}

func (c *scapContentDataStream) getExtendedProfileFromTailoring(ds *xmlquery.Node, tailoredProfile string) string {
	nodes := ds.SelectElements("//xccdf-1.2:Profile")
	for _, node := range nodes {
		tailoredProfileID := node.SelectAttr("id")
		if tailoredProfileID != tailoredProfile {
			continue
		}

		profileID := node.SelectAttr("extends")
		if profileID != "" {
			return profileID
		}
	}
	return ""
}

func (c *scapContentDataStream) FetchResources() ([]string, error) {
	found, warnings, err := fetch(c.client, c.resources)
	if err != nil {
		return warnings, err
	}
	c.found = found
	return warnings, nil
}

func fetch(client *kubernetes.Clientset, objects []string) (map[string][]byte, []string, error) {
	warnings := []string{}
	results := map[string][]byte{}
	for _, uri := range objects {
		err := func() error {
			LOG("Fetching URI: '%s'", uri)
			req := client.RESTClient().Get().RequestURI(uri)
			stream, err := req.Stream(context.TODO())
			if meta.IsNoMatchError(err) || kerrors.IsForbidden(err) || kerrors.IsNotFound(err) {
				DBG("Encountered non-fatal error to be persisted in the scan: %s", err)
				objerr := fmt.Errorf("could not fetch %s: %w", uri, err)
				warnings = append(warnings, objerr.Error())
				return nil
			} else if err != nil {
				return err
			}
			defer stream.Close()
			body, err := ioutil.ReadAll(stream)
			if err != nil {
				return err
			}
			if len(body) == 0 {
				DBG("no data in request body")
				return nil
			}
			yamlBody, err := yaml.JSONToYAML(body)
			if err != nil {
				return err
			}
			results[uri] = yamlBody
			return nil
		}()
		if err != nil {
			return nil, warnings, err
		}
	}
	return results, warnings, nil
}

func (c *scapContentDataStream) SaveWarningsIfAny(warnings []string, outputFile string) error {
	// No warnings to persist
	if warnings == nil || len(warnings) == 0 {
		return nil
	}
	DBG("Persisting warnings to output file")
	warningsStr := strings.Join(warnings, "\n")
	err := ioutil.WriteFile(outputFile, []byte(warningsStr), 0600)
	return err
}

func (c *scapContentDataStream) SaveResources(to string) error {
	return saveResources(to, c.found)
}

func saveResources(rootDir string, data map[string][]byte) error {
	for apiPath, fileContents := range data {
		saveDir, saveFile, err := getSaveDirectoryAndFileName(rootDir, apiPath)
		savePath := path.Join(saveDir, saveFile)
		LOG("Saving fetched resource to: '%s'", savePath)
		if err != nil {
			return err
		}
		err = os.MkdirAll(saveDir, 0700)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(savePath, fileContents, 0600)
		if err != nil {
			return err
		}
	}
	return nil
}

// Returns the absolute directory path (including rootDir) and filename for the given apiPath.
func getSaveDirectoryAndFileName(rootDir string, apiPath string) (string, string, error) {
	base := path.Base(apiPath)
	if base == "." || base == "/" {
		return "", "", fmt.Errorf("bad object path: %s", apiPath)
	}
	subDirs := path.Dir(apiPath)
	if subDirs == "." {
		return "", "", fmt.Errorf("bad object path: %s", apiPath)
	}

	return path.Join(rootDir, subDirs), base, nil
}
