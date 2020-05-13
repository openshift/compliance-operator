package main

import (
	"fmt"
	"os"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

type complianceCrClient struct {
	client runtimeclient.Client
	scheme *runtime.Scheme
}

func createCrClient(config *rest.Config) (*complianceCrClient, error) {
	scheme := runtime.NewScheme()

	corev1.AddToScheme(scheme)
	mcfgv1.AddToScheme(scheme)

	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&compv1alpha1.ComplianceRemediation{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&compv1alpha1.ComplianceScan{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&compv1alpha1.ComplianceScanList{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&compv1alpha1.ComplianceSuite{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&compv1alpha1.ComplianceCheckResult{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&metav1.CreateOptions{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&metav1.UpdateOptions{})
	scheme.AddKnownTypes(compv1alpha1.SchemeGroupVersion,
		&metav1.ListOptions{})

	client, err := runtimeclient.New(config, runtimeclient.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, err
	}

	return &complianceCrClient{
		client: client,
		scheme: scheme,
	}, nil
}

func getValidStringArg(cmd *cobra.Command, name string) string {
	val, _ := cmd.Flags().GetString(name)
	if val == "" {
		fmt.Fprintf(os.Stderr, "The command line argument '%s' is mandatory.\n", name)
		os.Exit(1)
	}
	return val
}
