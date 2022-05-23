package framework

import (
	"flag"
	"log"
	"os"
	"testing"
)

func MainEntry(m *testing.M) {
	fopts := &frameworkOpts{}
	fopts.addToFlagSet(flag.CommandLine)
	// controller-runtime registers the --kubeconfig flag in client config
	// package:
	// https://github.com/kubernetes-sigs/controller-runtime/blob/v0.5.2/pkg/client/config/config.go#L39
	//
	// If this flag is not registered, do so. Otherwise retrieve its value.
	kcFlag := flag.Lookup(KubeConfigFlag)
	if kcFlag == nil {
		flag.StringVar(&fopts.kubeconfigPath, KubeConfigFlag, "", "path to kubeconfig")
	}

	flag.Parse()

	if kcFlag != nil {
		fopts.kubeconfigPath = kcFlag.Value.String()
	}

	f, err := newFramework(fopts)
	if err != nil {
		log.Fatalf("Failed to create framework: %v", err)
	}

	Global = f

	exitCode, err := f.runM(m)
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(exitCode)
}
