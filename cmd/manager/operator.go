package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	ocpapi "github.com/openshift/api"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	kubemetrics "github.com/operator-framework/operator-sdk/pkg/kube-metrics"
	"github.com/operator-framework/operator-sdk/pkg/leader"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/operator-framework/operator-sdk/pkg/metrics"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"

	"github.com/openshift/compliance-operator/pkg/apis"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
)

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "The compliance-operator command",
	Long:  `An operator that issues compliance checks and their lifecycle.`,
	Run:   RunOperator,
}

func init() {
	rootCmd.AddCommand(operatorCmd)
}

// Change below variables to serve metrics on different host or port.
var (
	metricsHost               = "0.0.0.0"
	metricsPort         int32 = 8383
	operatorMetricsPort int32 = 8686
	defaultProducts           = []string{
		"rhcos4",
		"ocp4",
	}
)

const (
	defaultScanSettingsName          = "default"
	defaultAutoApplyScanSettingsName = "default-auto-apply"
	// Run scan every day at 1am
	defaultScanSettingsSchedule = "0 1 * * *"
)

func printVersion() {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	log.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
}

func RunOperator(cmd *cobra.Command, args []string) {
	// Add the zap logger flag set to the CLI. The flag set must
	// be added before calling pflag.Parse().
	flags := cmd.Flags()
	flags.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	flags.AddGoFlagSet(flag.CommandLine)

	if err := flags.Parse(zap.FlagSet().Args()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse zap flagset: %v", zap.FlagSet().Args())
		os.Exit(1)
	}

	// Use a zap logr.Logger implementation. If none of the zap
	// flags are configured (or if the zap flag set is not being
	// used), this defaults to a production zap logger.
	//
	// The logger instantiated here can be changed to any logger
	// implementing the logr.Logger interface. This logger will
	// be propagated through the whole operator, generating
	// uniform and structured logs.
	logf.SetLogger(zap.Logger())

	printVersion()

	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}
	log.Info("Watching", "namespace", namespace)
	// Force watch of compliance-operator-namespace so it gets added to the cache
	if !strings.Contains(namespace, common.GetComplianceOperatorNamespace()) {
		namespace = namespace + "," + common.GetComplianceOperatorNamespace()
	}
	options := manager.Options{
		Namespace:          namespace,
		MetricsBindAddress: fmt.Sprintf("%s:%d", metricsHost, metricsPort),
	}
	namespaceList := strings.Split(namespace, ",")
	// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
	// Note that this is not intended to be used for excluding namespaces, this is better done via a Predicate
	// Also note that you may face performance issues when using this with a high number of namespaces.
	// More Info: https://godoc.org/github.com/kubernetes-sigs/controller-runtime/pkg/cache#MultiNamespacedCacheBuilder
	if strings.Contains(namespace, ",") {
		options.Namespace = ""
		options.NewCache = cache.MultiNamespacedCacheBuilder(namespaceList)
	}

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	ctx := context.TODO()
	// Become the leader before proceeding
	err = leader.Become(ctx, "compliance-operator-lock")
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(cfg, options)
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Registering Components.")

	mgrscheme := mgr.GetScheme()
	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgrscheme); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
	if err := mcfgapi.Install(mgrscheme); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	if err := ocpapi.Install(mgrscheme); err != nil {
		log.Info("Couldn't install OCP API. This is not fatal though.")
		log.Error(err, "")
	}

	// Index the ID field of Checks
	if err := mgr.GetFieldIndexer().IndexField(ctx, &compv1alpha1.ComplianceCheckResult{}, compv1alpha1.ComplianceRemediationDependencyField, func(rawObj k8sruntime.Object) []string {
		check, ok := rawObj.(*compv1alpha1.ComplianceCheckResult)
		if !ok {
			return []string{}
		}
		return []string{check.ID}
	}); err != nil {
		log.Error(err, "Error indexing the ID field of ComplianceCheckResult")
		os.Exit(1)
	}

	// Setup all Controllers
	if err := controller.AddToManager(mgr); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Add the Metrics Service
	addMetrics(ctx, cfg)

	if err := ensureDefaultProfileBundles(ctx, mgr.GetClient(), namespaceList); err != nil {
		log.Error(err, "Error creating default ProfileBundles.")
		os.Exit(1)
	}

	if err := ensureDefaultScanSettings(ctx, mgr.GetClient(), namespaceList); err != nil {
		log.Error(err, "Error creating default ScanSettings.")
		os.Exit(1)
	}

	log.Info("Starting the Cmd.")

	// Start the Cmd
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "Manager exited non-zero")
		os.Exit(1)
	}
}

// addMetrics will create the Services and Service Monitors to allow the operator export the metrics by using
// the Prometheus operator
func addMetrics(ctx context.Context, cfg *rest.Config) {
	// Get the namespace the operator is currently deployed in.
	operatorNs, err := k8sutil.GetOperatorNamespace()
	if err != nil {
		if errors.Is(err, k8sutil.ErrRunLocal) {
			log.Info("Skipping CR metrics server creation; not running in a cluster.")
			return
		}
	}

	if err := serveCRMetrics(cfg, operatorNs); err != nil {
		log.Info("Could not generate and serve custom resource metrics", "error", err.Error())
	}

	// Add to the below struct any other metrics ports you want to expose.
	servicePorts := []v1.ServicePort{
		{Port: metricsPort, Name: metrics.OperatorPortName, Protocol: v1.ProtocolTCP, TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: metricsPort}},
		{Port: operatorMetricsPort, Name: metrics.CRPortName, Protocol: v1.ProtocolTCP, TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: operatorMetricsPort}},
	}

	// Create Service object to expose the metrics port(s).
	service, err := metrics.CreateMetricsService(ctx, cfg, servicePorts)
	if err != nil {
		log.Info("Could not create metrics Service", "error", err.Error())
	}

	// CreateServiceMonitors will automatically create the prometheus-operator ServiceMonitor resources
	// necessary to configure Prometheus to scrape metrics from this operator.
	services := []*v1.Service{service}

	// The ServiceMonitor is created in the same namespace where the operator is deployed
	_, err = metrics.CreateServiceMonitors(cfg, operatorNs, services)
	if err != nil {
		log.Info("Could not create ServiceMonitor object", "error", err.Error())
		// If this operator is deployed to a cluster without the prometheus-operator running, it will return
		// ErrServiceMonitorNotPresent, which can be used to safely skip ServiceMonitor creation.
		if err == metrics.ErrServiceMonitorNotPresent {
			log.Info("Install prometheus-operator in your cluster to create ServiceMonitor objects", "error", err.Error())
		}
	}
}

func ensureDefaultProfileBundles(ctx context.Context, crclient client.Client, namespaceList []string) error {
	pbimg := utils.GetComponentImage(utils.CONTENT)
	var lastErr error
	for _, prod := range defaultProducts {
		for _, ns := range namespaceList {
			pb := &compv1alpha1.ProfileBundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      prod,
					Namespace: ns,
				},
				Spec: compv1alpha1.ProfileBundleSpec{
					ContentImage: pbimg,
					ContentFile:  fmt.Sprintf("ssg-%s-ds.xml", prod),
				},
			}
			if err := ensureSupportedProfileBundle(ctx, crclient, pb); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

func ensureSupportedProfileBundle(ctx context.Context, crclient client.Client, pb *compv1alpha1.ProfileBundle) error {
	createErr := crclient.Create(ctx, pb)
	if k8serrors.IsAlreadyExists(createErr) {
		return crclient.Patch(ctx, pb, client.Merge)
	} else if createErr != nil {
		return createErr
	}
	return nil
}

func ensureDefaultScanSettings(ctx context.Context, crclient client.Client, namespaceList []string) error {
	var lastErr error
	for _, ns := range namespaceList {
		d := &compv1alpha1.ScanSetting{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultScanSettingsName,
				Namespace: ns,
			},
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				Schedule: defaultScanSettingsSchedule,
			},
			Roles: []string{
				"worker",
				"master",
			},
		}
		derr := crclient.Create(ctx, d)
		if !k8serrors.IsAlreadyExists(derr) {
			lastErr = derr
		}

		a := &compv1alpha1.ScanSetting{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultAutoApplyScanSettingsName,
				Namespace: ns,
			},
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations:  true,
				AutoUpdateRemediations: true,
				Schedule:               defaultScanSettingsSchedule,
			},
			Roles: []string{
				"worker",
				"master",
			},
		}
		aerr := crclient.Create(ctx, a)
		if !k8serrors.IsAlreadyExists(aerr) {
			lastErr = aerr
		}
	}
	return lastErr
}

// serveCRMetrics gets the Operator/CustomResource GVKs and generates metrics based on those types.
// It serves those metrics on "http://metricsHost:operatorMetricsPort".
func serveCRMetrics(cfg *rest.Config, operatorNs string) error {
	// The function below returns a list of filtered operator/CR specific GVKs. For more control, override the GVK list below
	// with your own custom logic. Note that if you are adding third party API schemas, probably you will need to
	// customize this implementation to avoid permissions issues.
	filteredGVK, err := k8sutil.GetGVKsFromAddToScheme(apis.AddToScheme)
	if err != nil {
		return err
	}

	// The metrics will be generated from the namespaces which are returned here.
	// NOTE that passing nil or an empty list of namespaces in GenerateAndServeCRMetrics will result in an error.
	ns, err := kubemetrics.GetNamespacesForMetrics(operatorNs)
	if err != nil {
		return err
	}

	// Generate and serve custom resource specific metrics.
	err = kubemetrics.GenerateAndServeCRMetrics(cfg, ns, filteredGVK, metricsHost, operatorMetricsPort)
	if err != nil {
		return err
	}
	return nil
}
