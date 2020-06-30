package main

import (
	"fmt"
	"os"
	"path/filepath"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	compapis "github.com/openshift/compliance-operator/pkg/apis"
)

const (
	maxRetries = 15
)

var log = logf.Log.WithName("cmd")

type complianceCrClient struct {
	client   runtimeclient.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func (crclient *complianceCrClient) useEventRecorder(source string, config *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartEventWatcher(
		func(e *corev1.Event) {
			log.Info(e.Type, "object", e.InvolvedObject, "reason", e.Reason, "message", e.Message)
		})
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	crclient.recorder = eventBroadcaster.NewRecorder(crclient.scheme, v1.EventSource{Component: source})
	return nil
}

func createCrClient(config *rest.Config) (*complianceCrClient, error) {
	scheme := runtime.NewScheme()

	corev1.AddToScheme(scheme)
	mcfgv1.AddToScheme(scheme)
	compapis.AddToScheme(scheme)

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

func readContent(filename string) (*os.File, error) {
	// gosec complains that the file is passed through an evironment variable. But
	// this is not a security issue because none of the files are user-provided
	cleanFileName := filepath.Clean(filename)
	// #nosec G304
	return os.Open(cleanFileName)
}
