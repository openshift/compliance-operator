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
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"
	"github.com/itchyny/gojq"
	"github.com/openshift/compliance-operator/pkg/utils"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/kubernetes"
)

const (
	contentFileTimeout = 3600
	valuePrefix        = "xccdf_org.ssgproject.content_value_"
)

var (
	MoreThanOneObjErr = errors.New("more than one object returned from the filter")
)

// For OpenSCAP content as an XML data stream. Implements ResourceFetcher.
type scapContentDataStream struct {
	// Client for Gets
	client *kubernetes.Clientset
	// Staging objects
	dataStream *xmlquery.Node
	tailoring  *xmlquery.Node
	resources  []utils.ResourcePath
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
	found := []utils.ResourcePath{
		{
			ObjPath:  "/version",
			DumpPath: "/version",
		},
		{
			ObjPath:  "/apis/config.openshift.io/v1/clusteroperators/openshift-apiserver",
			DumpPath: "/apis/config.openshift.io/v1/clusteroperators/openshift-apiserver",
		},
		{
			ObjPath:  "/apis/config.openshift.io/v1/infrastructures/cluster",
			DumpPath: "/apis/config.openshift.io/v1/infrastructures/cluster",
		},
		{
			ObjPath:  "/apis/config.openshift.io/v1/networks/cluster",
			DumpPath: "/apis/config.openshift.io/v1/networks/cluster",
		},
		{
			ObjPath:  "/api/v1/nodes",
			DumpPath: "/api/v1/nodes",
		},
	}
	effectiveProfile := profile
	var valuesList map[string]string

	if c.tailoring != nil {
		var selected []utils.ResourcePath
		selected, valuesList = getResourcePaths(c.tailoring, c.dataStream, profile, nil)
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

	selected, _ := getResourcePaths(c.dataStream, c.dataStream, effectiveProfile, valuesList)
	if len(selected) == 0 {
		fmt.Printf("no valid checks found in profile\n")
	}
	found = append(found, selected...)
	c.resources = found
	DBG("c.resources: %v\n", c.resources)
	return nil
}

// getPathsFromRuleWarning finds the API endpoint from in. The expected structure is:
//
//  <warning category="general" lang="en-US"><code class="ocp-api-endpoint">/apis/config.openshift.io/v1/oauths/cluster
//  </code></warning>
func getPathFromWarningXML(in *xmlquery.Node, valueList map[string]string) []utils.ResourcePath {
	DBG("Parsing warning %s", in.OutputXML(false))
	path, err := utils.GetPathFromWarningXML(in, valueList)
	if err != nil {
		LOG("Error occurred at parsing warning %s", in.OutputXML((false)))
		LOG("Error message: %s", err)
	}
	return path
}

// Collect the resource paths for objects that this scan needs to obtain.
// The profile will have a series of "selected" checks that we grab all of the path info from.
func getResourcePaths(profileDefs *xmlquery.Node, ruleDefs *xmlquery.Node, profile string, overrideValueList map[string]string) ([]utils.ResourcePath, map[string]string) {
	out := []utils.ResourcePath{}
	selectedChecks := []string{}

	// Before staring process, collect all of the variables in definitions.
	valuesList := make(map[string]string)
	defs := [...]*xmlquery.Node{ruleDefs, profileDefs}
	for _, def := range defs {
		allValues := xmlquery.Find(def, "//xccdf-1.2:Value")

		for _, variable := range allValues {
			for _, val := range variable.SelectElements("//xccdf-1.2:value") {
				if val.SelectAttr("hidden") == "true" {
					// this is typically used for functions
					continue
				}
				if val.SelectAttr("selector") == "" {
					// It is not an enum choice, but a default value instead
					if strings.HasPrefix(variable.SelectAttr("id"), valuePrefix) {
						valuesList[strings.TrimPrefix(variable.SelectAttr("id"), valuePrefix)] = html.UnescapeString(val.OutputXML(false))
					}
				}
			}
		}
		allSetValues := xmlquery.Find(def, "//xccdf-1.2:set-value")
		for _, variable := range allSetValues {
			if strings.HasPrefix(variable.SelectAttr("idref"), valuePrefix) {
				valuesList[strings.TrimPrefix(variable.SelectAttr("idref"), valuePrefix)] = html.UnescapeString(variable.OutputXML(false))
			}
		}
	}

	// override variables which is defined in tailored profile
	if overrideValueList != nil {
		for k, v := range overrideValueList {
			if _, exists := valuesList[k]; exists {
				valuesList[k] = v
			}
		}
	}

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
		return out, valuesList
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
			apiPaths := getPathFromWarningXML(warn, valuesList)
			if len(apiPaths) == 0 {
				continue
			}
			// We only care for the first occurrence that works
			out = append(out, apiPaths...)
			warningFound = true
			break
		}

		if !warningFound {
			DBG("Couldn't find 'warning' child of check %s", checkID)
			continue
		}

	}
	return out, valuesList
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
	found, warnings, err := fetch(func(uri string) (io.ReadCloser, error) {
		return c.client.RESTClient().Get().RequestURI(uri).Stream(context.Background())
	}, c.resources)
	if err != nil {
		return warnings, err
	}
	c.found = found
	return warnings, nil
}

func fetch(streamFunc func(string) (io.ReadCloser, error), objects []utils.ResourcePath) (map[string][]byte, []string, error) {
	var warnings []string
	results := map[string][]byte{}
	ctx := context.Background()
	for _, rpath := range objects {
		err := func() error {
			uri := rpath.ObjPath
			LOG("Fetching URI: '%s'", uri)
			stream, err := streamFunc(uri)
			if meta.IsNoMatchError(err) || kerrors.IsForbidden(err) || kerrors.IsNotFound(err) {
				DBG("Encountered non-fatal error to be persisted in the scan: %s", err)
				objerr := fmt.Errorf("could not fetch %s: %w", uri, err)
				warnings = append(warnings, objerr.Error())
				// for 404s we'll add a warning comment in the object so openSCAP can read and process it
				if kerrors.IsNotFound(err) {
					results[rpath.DumpPath] = []byte("# kube-api-error=" + kerrors.ReasonForError(err))
				}
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
			if rpath.Filter != "" {
				DBG("Applying filter '%s' to path '%s'", rpath.Filter, rpath.ObjPath)
				filteredBody, filterErr := filter(ctx, body, rpath.Filter)
				if errors.Is(filterErr, MoreThanOneObjErr) {
					warnings = append(warnings, filterErr.Error())
				} else if filterErr != nil {
					return fmt.Errorf("Couldn't filter: %w", filterErr)
				}
				results[rpath.DumpPath] = filteredBody
			} else {
				results[rpath.DumpPath] = body
			}
			return nil
		}()
		if err != nil {
			return nil, warnings, err
		}
	}
	return results, warnings, nil
}

func filter(ctx context.Context, rawobj []byte, filter string) ([]byte, error) {
	fltr, fltrErr := gojq.Parse(filter)
	if fltrErr != nil {
		return nil, fmt.Errorf("could not create filter '%s': %w", filter, fltrErr)
	}
	obj := map[string]interface{}{}
	unmarshallErr := json.Unmarshal(rawobj, &obj)
	if unmarshallErr != nil {
		return nil, fmt.Errorf("Error unmarshalling json: %w", unmarshallErr)
	}
	iter := fltr.RunWithContext(ctx, obj)
	v, ok := iter.Next()
	if !ok {
		DBG("No result from filter. This is an issue and an error will be returned.")
		return nil, fmt.Errorf("couldn't get filtered object")
	}
	if err, ok := v.(error); ok {
		DBG("Error while filtering: %s", err)
		return nil, err
	}

	out, marshallErr := json.Marshal(&v)
	if marshallErr != nil {
		return nil, fmt.Errorf("Error marshalling json: %w", marshallErr)
	}
	_, isNotEOF := iter.Next()
	if isNotEOF {
		DBG("No more results should have come from the filter. This is an issue with the content.")
		return out, fmt.Errorf("Skipping extra results from filter '%s': %w", filter, MoreThanOneObjErr)
	}
	return out, nil
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
