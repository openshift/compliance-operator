package main

import (
	"context"
	"fmt"
	"os"

	backoff "github.com/cenkalti/backoff/v3"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const maxScanUpdateRetries = 5

var rerunnerCmd = &cobra.Command{
	Use:   "suitererunner",
	Short: "Re-runs a ComplianceSuite",
	Long:  `makes sure that a ComplianceSuite's scans get re-run.`,
	Run:   RerunSuite,
}

func init() {
	defineRerunnerFlags(rerunnerCmd)
}

type rerunnerconfig struct {
	Name      string
	Namespace string
	client    *complianceCrClient
}

func defineRerunnerFlags(cmd *cobra.Command) {
	cmd.Flags().String("name", "", "The name of the ComplianceSuite to be re-run")
	cmd.Flags().String("namespace", "", "The namespace of the ComplianceSuite to be re-run")
}

func getRerunnerConfig(cmd *cobra.Command) *rerunnerconfig {
	var conf rerunnerconfig
	conf.Name = getValidStringArg(cmd, "name")
	conf.Namespace = getValidStringArg(cmd, "namespace")

	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Printf("Can't create incluster config: %v\n", err)
		os.Exit(1)
	}

	crclient, err := createCrClient(config)
	if err != nil {
		fmt.Printf("Cannot create client for our types: %v\n", err)
		os.Exit(1)
	}
	conf.client = crclient
	return &conf
}

func RerunSuite(cmd *cobra.Command, args []string) {
	conf := getRerunnerConfig(cmd)

	scans := &compv1alpha1.ComplianceScanList{}
	scanSuiteSelector := make(map[string]string)
	scanSuiteSelector[compv1alpha1.SuiteLabel] = conf.Name
	listOpts := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(scanSuiteSelector),
		Namespace:     conf.Namespace,
	}
	err := conf.client.client.List(context.TODO(), scans, listOpts)
	if err != nil {
		fmt.Printf("Error while getting scans for ComplianceSuite '%s', err: %s\n", conf.Name, err)
		os.Exit(1)
	}

	fmt.Printf("Got %d scans from the ComplianceSuite '%s'\n", len(scans.Items), conf.Name)

	for _, scan := range scans.Items {
		currentScan := &scan
		key := types.NamespacedName{Name: scan.GetName(), Namespace: scan.GetNamespace()}
		err := backoff.Retry(func() error {
			var scanCopy *compv1alpha1.ComplianceScan
			if currentScan == nil {
				fmt.Printf("Re-fetching ComplianceScan '%s' since the reference we had is no longer valid\n", scanCopy.Name)
				err = conf.client.client.Get(context.TODO(), key, currentScan)
				if err != nil {
					fmt.Printf("Error re-fetching ComplianceScan '%s': %s\n", scanCopy.Name, err)
					return err
				}
			} else {
				scanCopy = currentScan.DeepCopy()
			}
			if scanCopy.Annotations == nil {
				scanCopy.Annotations = make(map[string]string)
			}
			scanCopy.Annotations[compv1alpha1.ComplianceScanRescanAnnotation] = ""

			fmt.Printf("Re-running ComplianceScan '%s'\n", scanCopy.Name)
			err := conf.client.client.Update(context.TODO(), scanCopy)
			if err != nil && (errors.IsResourceExpired(err) || errors.IsConflict(err)) {
				currentScan = nil
				return err
			} else if err != nil {
				fmt.Printf("Error while updating scan '%s', err: %s\n", scanCopy.Name, err)
				fmt.Printf("Retrying.\n")
				return err
			}
			return nil
		}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxScanUpdateRetries))

		if err != nil {
			fmt.Printf("Couldn't update scan '%s', err: %s\n", scan.Name, err)
			os.Exit(1)
		}
	}
}
