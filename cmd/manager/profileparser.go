package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	"github.com/antchfx/xmlquery"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/spf13/cobra"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cmpv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/profileparser"
)

var profileparserCmd = &cobra.Command{
	Use:   "profileparser",
	Short: "Runs the profile parser",
	Long:  `The profileparser reads a data stream file and generates profile objects from it.`,
	Run:   runProfileParser,
}

func init() {
	rootCmd.AddCommand(profileparserCmd)
	defineProfileParserFlags(profileparserCmd)
}

func defineProfileParserFlags(cmd *cobra.Command) {
	cmd.Flags().String("ds-path", "/content/ssg-ocp4-ds.xml", "Path to the datastream xml file")
	cmd.Flags().String("name", "", "Name of the ProfileBundle object")
	cmd.Flags().String("namespace", "", "Namespace of the ProfileBundle object")

	flags := cmd.Flags()
	flags.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	flags.AddGoFlagSet(flag.CommandLine)
}

func newParserConfig(cmd *cobra.Command) *profileparser.ParserConfig {
	pcfg := profileparser.ParserConfig{}

	flags := cmd.Flags()
	if err := flags.Parse(zap.FlagSet().Args()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse zap flagset: %v", zap.FlagSet().Args())
		os.Exit(1)
	}

	pcfg.DataStreamPath = getValidStringArg(cmd, "ds-path")
	pcfg.ProfileBundleKey.Name = getValidStringArg(cmd, "name")
	pcfg.ProfileBundleKey.Namespace = getValidStringArg(cmd, "namespace")

	logf.SetLogger(zap.Logger())

	printVersion()

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	crclient, err := createCrClient(cfg)
	if err != nil {
		fmt.Printf("Can't kubernetes client: %v\n", err)
		os.Exit(1)
	}
	pcfg.Scheme = crclient.scheme
	pcfg.Client = crclient.client

	return &pcfg
}

func getProfileBundle(pcfg *profileparser.ParserConfig) (*cmpv1alpha1.ProfileBundle, error) {
	pb := cmpv1alpha1.ProfileBundle{}

	err := pcfg.Client.Get(context.TODO(), pcfg.ProfileBundleKey, &pb)
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	return &pb, nil
}

// updateProfileBundleStatus updates the status of the given ProfileBundle. If
// the given error is nil, the status will be valid, else it'll be invalid
func updateProfileBundleStatus(pcfg *profileparser.ParserConfig, pb *cmpv1alpha1.ProfileBundle, err error) {
	if err != nil {
		// Never update a fetched object, always just a copy
		pbCopy := pb.DeepCopy()
		pbCopy.Status.DataStreamStatus = cmpv1alpha1.DataStreamInvalid
		pbCopy.Status.ErrorMessage = err.Error()
		pbCopy.Status.SetConditionInvalid()
		err = pcfg.Client.Status().Update(context.TODO(), pbCopy)
		if err != nil {
			log.Error(err, "Couldn't update ProfileBundle status")
			os.Exit(1)
		}
	} else {
		// Never update a fetched object, always just a copy
		pbCopy := pb.DeepCopy()
		pbCopy.Status.DataStreamStatus = cmpv1alpha1.DataStreamValid
		pbCopy.Status.SetConditionReady()
		err = pcfg.Client.Status().Update(context.TODO(), pbCopy)
		if err != nil {
			log.Error(err, "Couldn't update ProfileBundle status")
			os.Exit(1)
		}
	}
}

func runProfileParser(cmd *cobra.Command, args []string) {
	pcfg := newParserConfig(cmd)

	pb, err := getProfileBundle(pcfg)
	if err != nil {
		log.Error(err, "Couldn't get ProfileBundle")

		os.Exit(1)
	}

	contentFile, err := readContent(pcfg.DataStreamPath)
	if err != nil {
		log.Error(err, "Couldn't read the content")
		updateProfileBundleStatus(pcfg, pb, fmt.Errorf("Couldn't read content file: %s", err))
		os.Exit(1)
	}
	bufContentFile := bufio.NewReader(contentFile)
	contentDom, err := xmlquery.Parse(bufContentFile)
	if err != nil {
		log.Error(err, "Couldn't read the content XML")
		updateProfileBundleStatus(pcfg, pb, fmt.Errorf("Couldn't read content XML: %s", err))
		if closeErr := contentFile.Close(); closeErr != nil {
			log.Error(err, "Couldn't close the content file")
		}
		os.Exit(1)
	}

	err = profileparser.ParseBundle(contentDom, pb, pcfg)

	// The err variable might be nil, this is fine, it'll just update the status
	// to valid
	updateProfileBundleStatus(pcfg, pb, err)

	if closeErr := contentFile.Close(); closeErr != nil {
		log.Error(err, "Couldn't close the content file")
	}
}
