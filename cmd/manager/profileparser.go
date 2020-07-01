package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/spf13/cobra"
	"github.com/subchen/go-xmldom"
	"k8s.io/apimachinery/pkg/api/errors"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

// XMLDocument is a wrapper that keeps the interface XML-parser-agnostic
type XMLDocument struct {
	*xmldom.Document
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
		err = pcfg.Client.Status().Update(context.TODO(), pbCopy)
		if err != nil {
			log.Error(err, "Couldn't update ProfileBundle status")
			os.Exit(1)
		}
	} else {
		// Never update a fetched object, always just a copy
		pbCopy := pb.DeepCopy()
		pbCopy.Status.DataStreamStatus = cmpv1alpha1.DataStreamValid
		err = pcfg.Client.Status().Update(context.TODO(), pbCopy)
		if err != nil {
			log.Error(err, "Couldn't update ProfileBundle status")
			os.Exit(1)
		}
	}
}

func runProfileParser(cmd *cobra.Command, args []string) {
	exitSignal := make(chan os.Signal)
	signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)
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
	// #nosec
	defer contentFile.Close()
	bufContentFile := bufio.NewReader(contentFile)
	contentDom, err := xmldom.Parse(bufContentFile)
	if err != nil {
		log.Error(err, "Couldn't read the content XML")
		updateProfileBundleStatus(pcfg, pb, fmt.Errorf("Couldn't read content XML: %s", err))
		os.Exit(1)
	}

	err = profileparser.ParseProfilesAndDo(contentDom, pcfg, func(p *cmpv1alpha1.Profile) error {
		pCopy := p.DeepCopy()
		profileName := pCopy.Name

		if pCopy.Labels == nil {
			pCopy.Labels = make(map[string]string)
		}
		pCopy.Labels[cmpv1alpha1.ProfileBundleOwnerLabel] = pb.Name

		// overwrite name
		pCopy.SetName(profileparser.GetPrefixedName(pb.Name, profileName))

		if err := controllerutil.SetControllerReference(pb, pCopy, pcfg.Scheme); err != nil {
			return err
		}

		log.Info("Creating Profile", "Profile.name", p.Name)
		err := pcfg.Client.Create(context.TODO(), pCopy)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				log.Info("Profile already exists.", "Profile.Name", p.Name)
			} else {
				log.Error(err, "couldn't create profile")
				return err
			}
		}
		return nil
	})

	if err != nil {
		updateProfileBundleStatus(pcfg, pb, err)
		return
	}

	err = profileparser.ParseRulesAndDo(contentDom, pcfg, func(r *cmpv1alpha1.Rule) error {
		ruleName := r.Name
		// overwrite name
		r.SetName(profileparser.GetPrefixedName(pb.Name, ruleName))

		if r.Labels == nil {
			r.Labels = make(map[string]string)
		}
		r.Labels[cmpv1alpha1.ProfileBundleOwnerLabel] = pb.Name

		if r.Annotations == nil {
			r.Annotations = make(map[string]string)
		}
		r.Annotations[cmpv1alpha1.RuleIDAnnotationKey] = ruleName

		if err := controllerutil.SetControllerReference(pb, r, pcfg.Scheme); err != nil {
			return err
		}

		log.Info("Creating rule", "Rule.Name", r.Name)
		err := pcfg.Client.Create(context.TODO(), r)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				log.Info("Rule already exists.", "Rule.Name", r.Name)
			} else {
				log.Error(err, "couldn't create Rule")
				return err
			}
		}
		return nil
	})

	if err != nil {
		updateProfileBundleStatus(pcfg, pb, err)
		return
	}

	err = profileparser.ParseVariablesAndDo(contentDom, pcfg, func(v *cmpv1alpha1.Variable) error {
		varName := v.Name
		// overwrite name
		v.SetName(profileparser.GetPrefixedName(pb.Name, varName))

		if v.Labels == nil {
			v.Labels = make(map[string]string)
		}
		v.Labels[cmpv1alpha1.ProfileBundleOwnerLabel] = pb.Name

		if err := controllerutil.SetControllerReference(pb, v, pcfg.Scheme); err != nil {
			return err
		}

		log.Info("Creating variable", "Variable.Name", v.Name)
		err := pcfg.Client.Create(context.TODO(), v)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				log.Info("Variable already exists.", "Variable.Name", v.Name)
			} else {
				log.Error(err, "couldn't create Variable")
				return err
			}
		}
		return nil
	})

	// The err variable might be nil, this is fine, it'll just update the status
	// to valid
	updateProfileBundleStatus(pcfg, pb, err)

	<-exitSignal
}
