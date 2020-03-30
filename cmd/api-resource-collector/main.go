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
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ResourceFetcher sources content for resource paths to gather, and then saves the path contents.
// This interface is provided primarily for code organization.
type ResourceFetcher interface {
	// Load from a source path, including the decoding step.
	LoadSource(path string) error
	// Search the decoded data for the resources we need under a particular profile.
	FigureResources(profile string) error
	// Fetch the resources.
	FetchResources() error
	// Save the resources.
	SaveResources(to string) error
}

type fetcherConfig struct {
	Content   string
	ResultDir string
	Profile   string
	Local     bool
}

func defineFlags(cmd *cobra.Command) {
	cmd.Flags().String("content", "", "The path to the OpenSCAP content file.")
	cmd.Flags().String("resultdir", "", "The directory to write the collected object files to.")
	cmd.Flags().String("profile", "", "The scan profile.")
	cmd.Flags().Bool("local", false, "Uses KUBECONFIG instead of the in-cluster config, for running the program locally.")
	cmd.Flags().Bool("debug", false, "Print debug messages.")
}

func parseConfig(cmd *cobra.Command) *fetcherConfig {
	var conf fetcherConfig
	conf.Content = getValidStringArg(cmd, "content")
	conf.ResultDir = getValidStringArg(cmd, "resultdir")
	conf.Profile = getValidStringArg(cmd, "profile")
	local, err := cmd.Flags().GetBool("local")
	if err != nil {
		fmt.Printf("error fetching local flag")
		os.Exit(1)
	}
	conf.Local = local
	debugLog, _ = cmd.Flags().GetBool("debug")
	return &conf
}

func getValidStringArg(cmd *cobra.Command, name string) string {
	val, _ := cmd.Flags().GetString(name)
	if val == "" {
		fmt.Fprintf(os.Stderr, "The command line argument '%s' is mandatory.\n", name)
		os.Exit(1)
	}
	return val
}

// getConfig returns the rest config, from KUBECONFIG if local or in-cluster if !local
func getConfig(local bool) *rest.Config {
	if local {
		DBG("Running locally from KUBECONFIG")

		kubeConfigFile := os.Getenv("KUBECONFIG")
		if len(kubeConfigFile) == 0 {
			FATAL("Set KUBECONFIG when running locally")
		}

		conf, err := clientcmd.BuildConfigFromFlags("", kubeConfigFile)
		if err != nil {
			FATAL("Error building config: %v", err)
		}
		return conf
	}

	DBG("Running in-cluster")
	conf, err := rest.InClusterConfig()
	if err != nil {
		FATAL("Error getting in-cluster config: %v", err)
	}
	return conf
}

func run(cmd *cobra.Command, args []string) {
	fetcherConf := parseConfig(cmd)
	kubeClient, err := kubernetes.NewForConfig(getConfig(fetcherConf.Local))
	if err != nil {
		FATAL("Error building kubeClient: %v", err)
	}

	fetcher := &scapContentDataStream{
		client: kubeClient,
	}

	if err := fetcher.LoadSource(fetcherConf.Content); err != nil {
		FATAL("Error loading source data: %v", err)
	}

	if err := fetcher.FigureResources(fetcherConf.Profile); err != nil {
		FATAL("Error finding resources: %v", err)
	}

	if err := fetcher.FetchResources(); err != nil {
		FATAL("Error fetching resources: %v", err)
	}

	if err := fetcher.SaveResources(fetcherConf.ResultDir); err != nil {
		FATAL("Error saving resources: %v", err)
	}
}

var rootCmd = &cobra.Command{
	Use:   "api-resource-collector",
	Short: "Stages cluster resources for OpenSCAP scanning.",
	Long:  "Stages cluster resources for OpenSCAP scanning.",
	Run:   run,
}

func main() {
	defineFlags(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		FATAL("%v", err)
	}

	os.Exit(0)
}
