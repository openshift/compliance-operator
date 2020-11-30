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
	"flag"

	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var apiResourceCollectorCmd = &cobra.Command{
	Use:   "api-resource-collector",
	Short: "Stages cluster resources for OpenSCAP scanning.",
	Long:  "Stages cluster resources for OpenSCAP scanning.",
	Run:   runAPIResourceCollector,
}

func init() {
	rootCmd.AddCommand(apiResourceCollectorCmd)
	defineAPIResourceCollectorFlags(apiResourceCollectorCmd)
}

// ResourceFetcher sources content for resource paths to gather, and then saves the path contents.
// This interface is provided primarily for code organization.
type ResourceFetcher interface {
	// Load from a source path, including the decoding step.
	LoadSource(path string) error
	// Load from a tailoring path, including the decoding step.
	LoadTailoring(path string) error
	// Search the decoded data for the resources we need under a particular profile.
	FigureResources(profile string) error
	// Fetch the resources.
	FetchResources() ([]string, error)
	// Save warnings
	SaveWarningsIfAny([]string, string) error
	// Save the resources.
	SaveResources(to string) error
}

type fetcherConfig struct {
	Content            string
	Tailoring          string
	ResultDir          string
	Profile            string
	ExitCodeFile       string
	WarningsOutputFile string
}

func defineAPIResourceCollectorFlags(cmd *cobra.Command) {
	cmd.Flags().String("content", "", "The path to the OpenSCAP content file.")
	cmd.Flags().String("tailoring", "", "The path to the OpenSCAP tailoring file.")
	cmd.Flags().String("resultdir", "", "The directory to write the collected object files to.")
	cmd.Flags().String("profile", "", "The scan profile.")
	cmd.Flags().String("warnings-output-file", "", "A file containing the warnings output.")
	cmd.Flags().Bool("debug", false, "Print debug messages.")

	flags := cmd.Flags()
	flags.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	flags.AddGoFlagSet(flag.CommandLine)
}

func parseAPIResourceCollectorConfig(cmd *cobra.Command) *fetcherConfig {
	var conf fetcherConfig
	conf.Content = getValidStringArg(cmd, "content")
	conf.ResultDir = getValidStringArg(cmd, "resultdir")
	conf.Profile = getValidStringArg(cmd, "profile")
	conf.WarningsOutputFile = getValidStringArg(cmd, "warnings-output-file")
	debugLog, _ = cmd.Flags().GetBool("debug")
	conf.Tailoring, _ = cmd.Flags().GetString("tailoring")
	return &conf
}

func getConfig() *rest.Config {
	cfg, err := config.GetConfig()
	if err != nil {
		FATAL("Error getting kube cfg: %v", err)
	}
	return cfg
}

func runAPIResourceCollector(cmd *cobra.Command, args []string) {
	fetcherConf := parseAPIResourceCollectorConfig(cmd)
	kubeClient, err := kubernetes.NewForConfig(getConfig())
	if err != nil {
		FATAL("Error building kubeClient: %v", err)
	}

	fetcher := NewDataStreamResourceFetcher(kubeClient)

	if err := fetcher.LoadSource(fetcherConf.Content); err != nil {
		FATAL("Error loading source data: %v", err)
	}
	if fetcherConf.Tailoring != "" {
		if err := fetcher.LoadTailoring(fetcherConf.Tailoring); err != nil {
			FATAL("Error loading tailoring data: %v", err)
		}
	}
	if err := fetcher.FigureResources(fetcherConf.Profile); err != nil {
		FATAL("Error finding resources: %v", err)
	}
	warnings, err := fetcher.FetchResources()
	if warnErr := fetcher.SaveWarningsIfAny(warnings, fetcherConf.WarningsOutputFile); warnErr != nil {
		FATAL("Error writing warnings output file: %v", warnErr)
	}
	if err != nil {
		FATAL("Error fetching resources: %v", err)
	}

	if err := fetcher.SaveResources(fetcherConf.ResultDir); err != nil {
		FATAL("Error saving resources: %v", err)
	}
}
