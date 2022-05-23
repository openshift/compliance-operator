package manager

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	"github.com/antchfx/xmlquery"
	"github.com/spf13/cobra"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	cmpv1alpha1 "github.com/ComplianceAsCode/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/ComplianceAsCode/compliance-operator/pkg/profileparser"
)

var ProfileparserCmd = &cobra.Command{
	Use:   "profileparser",
	Short: "Runs the profile parser",
	Long:  `The profileparser reads a data stream file and generates profile objects from it.`,
	Run:   runProfileParser,
}

func init() {
	defineProfileParserFlags(ProfileparserCmd)
}

func defineProfileParserFlags(cmd *cobra.Command) {
	cmd.Flags().String("ds-path", "/content/ssg-ocp4-ds.xml", "Path to the datastream xml file")
	cmd.Flags().String("name", "", "Name of the ProfileBundle object")
	cmd.Flags().String("namespace", "", "Namespace of the ProfileBundle object")

	flags := cmd.Flags()

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	flags.AddGoFlagSet(flag.CommandLine)
}

func newParserConfig(cmd *cobra.Command) *profileparser.ParserConfig {
	pcfg := profileparser.ParserConfig{}

	flags := cmd.Flags()
	flags.AddGoFlagSet(flag.CommandLine)

	pcfg.DataStreamPath = getValidStringArg(cmd, "ds-path")
	pcfg.ProfileBundleKey.Name = getValidStringArg(cmd, "name")
	pcfg.ProfileBundleKey.Namespace = getValidStringArg(cmd, "namespace")

	logf.SetLogger(zap.New())

	printVersion()

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		cmdLog.Error(err, "")
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
		cmdLog.Error(err, "")
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
			cmdLog.Error(err, "Couldn't update ProfileBundle status")
			os.Exit(1)
		}
	} else {
		// Never update a fetched object, always just a copy
		pbCopy := pb.DeepCopy()
		pbCopy.Status.DataStreamStatus = cmpv1alpha1.DataStreamValid
		pbCopy.Status.SetConditionReady()
		err = pcfg.Client.Status().Update(context.TODO(), pbCopy)
		if err != nil {
			cmdLog.Error(err, "Couldn't update ProfileBundle status")
			os.Exit(1)
		}
	}
}

func runProfileParser(cmd *cobra.Command, args []string) {
	pcfg := newParserConfig(cmd)

	pb, err := getProfileBundle(pcfg)
	if err != nil {
		cmdLog.Error(err, "Couldn't get ProfileBundle")

		os.Exit(1)
	}

	contentFile, err := readContent(pcfg.DataStreamPath)
	if err != nil {
		cmdLog.Error(err, "Couldn't read the content")
		updateProfileBundleStatus(pcfg, pb, fmt.Errorf("Couldn't read content file: %s", err))
		os.Exit(1)
	}
	bufContentFile := bufio.NewReader(contentFile)
	contentDom, err := xmlquery.Parse(bufContentFile)
	if err != nil {
		cmdLog.Error(err, "Couldn't read the content XML")
		updateProfileBundleStatus(pcfg, pb, fmt.Errorf("Couldn't read content XML: %s", err))
		if closeErr := contentFile.Close(); closeErr != nil {
			cmdLog.Error(err, "Couldn't close the content file")
		}
		os.Exit(1)
	}

	err = profileparser.ParseBundle(contentDom, pb, pcfg)

	// The err variable might be nil, this is fine, it'll just update the status
	// to valid
	updateProfileBundleStatus(pcfg, pb, err)

	if err != nil {
		cmdLog.Error(err, "Parsing the bundle failed, will restart the container")
		os.Exit(1)
	}

	if closeErr := contentFile.Close(); closeErr != nil {
		cmdLog.Error(err, "Couldn't close the content file")
	}
}
