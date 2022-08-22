package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	monitoring "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	monclientv1 "github.com/coreos/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	ocpapi "github.com/openshift/api"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	kubemetrics "github.com/operator-framework/operator-sdk/pkg/kube-metrics"
	"github.com/operator-framework/operator-sdk/pkg/leader"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/operator-framework/operator-sdk/pkg/metrics"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
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
	ctrlMetrics "github.com/openshift/compliance-operator/pkg/controller/metrics"
	"github.com/openshift/compliance-operator/pkg/utils"
	"github.com/openshift/compliance-operator/version"
)

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "The compliance-operator command",
	Long:  `An operator that issues compliance checks and their lifecycle.`,
	Run:   RunOperator,
}

func init() {
	rootCmd.AddCommand(operatorCmd)
	defineOperatorFlags(operatorCmd)
}

type PlatformType string

const (
	PlatformOpenShift PlatformType = "OpenShift"
	PlatformEKS       PlatformType = "EKS"
	PlatformGeneric   PlatformType = "Generic"
	PlatformUnknown   PlatformType = "Unknown"
)

// Change below variables to serve metrics on different host or port.
var (
	metricsHost                      = "0.0.0.0"
	metricsServiceName               = "metrics"
	metricsPort                int32 = 8383
	operatorMetricsPort        int32 = 8686
	defaultProductsPerPlatform       = map[PlatformType][]string{
		PlatformOpenShift: {
			"rhcos4",
			"ocp4",
		},
		PlatformEKS: {
			"eks",
		},
	}
	defaultRolesPerPlatform = map[PlatformType][]string{
		PlatformOpenShift: {
			"master",
			"worker",
		},
		PlatformGeneric: {
			compv1alpha1.AllRoles,
		},
	}
	serviceMonitorBearerTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	serviceMonitorTLSCAFile       = "/etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt"
	alertName                     = "compliance"
)

const (
	defaultScanSettingsName          = "default"
	defaultAutoApplyScanSettingsName = "default-auto-apply"
	// Run scan every day at 1am
	defaultScanSettingsSchedule = "0 1 * * *"
)

func defineOperatorFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("skip-metrics", false,
		"Skips adding metrics.")
	cmd.Flags().String("platform", "OpenShift",
		"Specifies the Platform the Compliance Operator is running on. "+
			"This will affect the defaults created.")

	// Add the zap logger flag set to the CLI. The flag set must
	// be added before calling pflag.Parse().
	flags := cmd.Flags()
	flags.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	flags.AddGoFlagSet(flag.CommandLine)

}

func printVersion() {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	log.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
	log.Info(fmt.Sprintf("Compliance Operator Version: %v", version.Version))
}

func RunOperator(cmd *cobra.Command, args []string) {
	flags := cmd.Flags()
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

	namespace, err := common.GetWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}
	if namespace != "" {
		log.Info("Watching", "namespace", namespace)
		// Force watch of compliance-operator-namespace so it gets added to the cache
		if !strings.Contains(namespace, common.GetComplianceOperatorNamespace()) {
			namespace = namespace + "," + common.GetComplianceOperatorNamespace()
		}
	} else {
		log.Info("Watching all namespaces")
	}
	options := manager.Options{
		Namespace:          namespace,
		MetricsBindAddress: fmt.Sprintf("%s:%d", metricsHost, metricsPort),
	}
	var namespaceList []string

	if namespace != "" {
		namespaceList = strings.Split(namespace, ",")
		// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
		// Note that this is not intended to be used for excluding namespaces, this is better done via a Predicate
		// Also note that you may face performance issues when using this with a high number of namespaces.
		// More Info: https://godoc.org/github.com/kubernetes-sigs/controller-runtime/pkg/cache#MultiNamespacedCacheBuilder
		if strings.Contains(namespace, ",") {
			options.Namespace = ""
			options.NewCache = cache.MultiNamespacedCacheBuilder(namespaceList)
		}
	} else {
		// NOTE(jaosorior): This will be used to set up the needed defaults
		namespaceList = []string{common.GetComplianceOperatorNamespace()}
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

	kubeClient := kubernetes.NewForConfigOrDie(cfg)
	monitoringClient := monclientv1.NewForConfigOrDie(cfg)

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

	met := ctrlMetrics.New()
	if err := met.Register(); err != nil {
		log.Error(err, "Error registering metrics")
		os.Exit(1)
	}

	si, getSIErr := getSchedulingInfo(ctx, mgr.GetAPIReader())
	if getSIErr != nil {
		log.Error(getSIErr, "Getting control plane scheduling info")
		os.Exit(1)
	}

	// Setup all Controllers
	if err := controller.AddToManager(mgr, met, si); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
	pflag, _ := flags.GetString("platform")
	platform := getValidPlatform(pflag)

	skipMetrics, _ := flags.GetBool("skip-metrics")
	// We only support these metrics in OpenShift (at the moment)
	if platform == PlatformOpenShift && !skipMetrics {
		// Add the Metrics Service
		addMetrics(ctx, cfg, kubeClient, monitoringClient)
	}

	if err := ensureDefaultProfileBundles(ctx, mgr.GetClient(), namespaceList, platform); err != nil {
		log.Error(err, "Error creating default ProfileBundles.")
		os.Exit(1)
	}

	if err := ensureDefaultScanSettings(ctx, mgr.GetClient(), namespaceList, platform, si); err != nil {
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

func getValidPlatform(p string) PlatformType {
	switch {
	case strings.EqualFold(p, string(PlatformOpenShift)):
		return PlatformOpenShift
	case strings.EqualFold(p, string(PlatformEKS)):
		return PlatformEKS
	default:
		return PlatformUnknown
	}
}

// addMetrics will create the Services and Service Monitors to allow the operator export the metrics by using
// the Prometheus operator
func addMetrics(ctx context.Context, cfg *rest.Config, kClient *kubernetes.Clientset,
	mClient *monclientv1.MonitoringV1Client) {
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

	// Create the metrics service and make sure the service-secret is available
	metricsService, err := ensureMetricsServiceAndSecret(ctx, kClient, operatorNs)
	if err != nil {
		log.Error(err, "Error creating metrics service/secret")
		os.Exit(1)
	}

	if err := handleServiceMonitor(ctx, cfg, mClient, operatorNs, metricsService); err != nil {
		log.Error(err, "Error creating ServiceMonitor")
		os.Exit(1)
	}

	if err := createNonComplianceAlert(ctx, mClient, operatorNs); err != nil {
		log.Error(err, "Error creating PrometheusRule")
		os.Exit(1)
	}
}

func operatorMetricService(ns string) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"name": "compliance-operator",
			},
			Annotations: map[string]string{
				"service.beta.openshift.io/serving-cert-secret-name": "compliance-operator-serving-cert",
			},
			Name:      "metrics",
			Namespace: ns,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:       "http-metrics",
					Port:       8383,
					TargetPort: intstr.FromInt(8383),
					Protocol:   v1.ProtocolTCP,
				},
				{
					Name:       "cr-metrics",
					Port:       8686,
					TargetPort: intstr.FromInt(8686),
					Protocol:   v1.ProtocolTCP,
				},
				{
					Name:       "metrics-co",
					Port:       8585,
					TargetPort: intstr.FromInt(8585),
					Protocol:   v1.ProtocolTCP,
				},
			},
			Selector: map[string]string{
				"name": "compliance-operator",
			},
			Type: v1.ServiceTypeClusterIP,
		},
	}
}

func ensureMetricsServiceAndSecret(ctx context.Context, kClient *kubernetes.Clientset, ns string) (*v1.Service, error) {
	var mService *v1.Service
	var err error
	mService, err = kClient.CoreV1().Services(ns).Create(ctx, operatorMetricService(ns), metav1.CreateOptions{})
	if err != nil && !kerr.IsAlreadyExists(err) {
		return nil, err
	}
	if kerr.IsAlreadyExists(err) {
		mService, err = kClient.CoreV1().Services(ns).Get(ctx, "metrics", metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
	}

	// Ensure the serving-cert secret for metrics is available, we have to exit and restart if not
	if _, err := kClient.CoreV1().Secrets(ns).Get(ctx, "compliance-operator-serving-cert", metav1.GetOptions{}); err != nil {
		if kerr.IsNotFound(err) {
			return nil, errors.New("compliance-operator-serving-cert not found - restarting, as the service may have just been created")
		} else {
			return nil, err
		}
	}

	return mService, nil
}

func getSchedulingInfo(ctx context.Context, cli client.Reader) (utils.CtlplaneSchedulingInfo, error) {
	key := types.NamespacedName{
		Name:      common.GetComplianceOperatorName(),
		Namespace: common.GetComplianceOperatorNamespace(),
	}
	pod := corev1.Pod{}
	log.Info("Deriving scheduling info from pod",
		"Pod.Name", key.Name, "Pod.Namespace", key.Namespace)
	if err := cli.Get(ctx, key, &pod); err != nil {
		return utils.CtlplaneSchedulingInfo{}, err
	}

	sel := pod.Spec.NodeSelector
	if sel == nil {
		sel = map[string]string{}
	}
	tol := pod.Spec.Tolerations
	if tol == nil {
		tol = []corev1.Toleration{}
	}

	return utils.CtlplaneSchedulingInfo{
		Selector:    sel,
		Tolerations: tol,
	}, nil
}

func ensureDefaultProfileBundles(
	ctx context.Context,
	crclient client.Client,
	namespaceList []string,
	platform PlatformType,
) error {
	pbimg := utils.GetComponentImage(utils.CONTENT)
	var lastErr error
	defaultProducts, isSupported := defaultProductsPerPlatform[platform]
	if !isSupported {
		log.Info("No ProfileBundle defaults for unknown product." +
			" Skipping defaults creation.")
		return nil
	}
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
			log.Info("Ensuring ProfileBundle is available",
				"ProfileBundle.Name", pb.GetName(),
				"ProfileBundle.Namespace", pb.GetNamespace())
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

func ensureDefaultScanSettings(
	ctx context.Context,
	crclient client.Client,
	namespaceList []string,
	platform PlatformType,
	si utils.CtlplaneSchedulingInfo,
) error {
	var lastErr error
	for _, ns := range namespaceList {
		roles := getDefaultRoles(platform)
		d := &compv1alpha1.ScanSetting{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultScanSettingsName,
				Namespace: ns,
			},
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				RawResultStorage: compv1alpha1.RawResultStorageSettings{
					NodeSelector: si.Selector,
					Tolerations:  si.Tolerations,
				},
			},
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				Schedule: defaultScanSettingsSchedule,
			},
			Roles: roles,
		}
		log.Info("Ensuring ScanSetting is available",
			"ScanSetting.Name", d.GetName(),
			"ScanSetting.Namespace", d.GetNamespace())
		derr := crclient.Create(ctx, d)
		if !k8serrors.IsAlreadyExists(derr) {
			lastErr = derr
		}

		a := &compv1alpha1.ScanSetting{
			ObjectMeta: metav1.ObjectMeta{
				Name:      defaultAutoApplyScanSettingsName,
				Namespace: ns,
			},
			ComplianceScanSettings: compv1alpha1.ComplianceScanSettings{
				RawResultStorage: compv1alpha1.RawResultStorageSettings{
					NodeSelector: si.Selector,
					Tolerations:  si.Tolerations,
				},
			},
			ComplianceSuiteSettings: compv1alpha1.ComplianceSuiteSettings{
				AutoApplyRemediations:  true,
				AutoUpdateRemediations: true,
				Schedule:               defaultScanSettingsSchedule,
			},
			Roles: roles,
		}
		log.Info("Ensuring ScanSetting is available",
			"ScanSetting.Name", d.GetName(),
			"ScanSetting.Namespace", d.GetNamespace())
		aerr := crclient.Create(ctx, a)
		if !k8serrors.IsAlreadyExists(aerr) {
			lastErr = aerr
		}
	}
	return lastErr
}

func getDefaultRoles(platform PlatformType) []string {
	roles, hasSpecific := defaultRolesPerPlatform[platform]
	if hasSpecific {
		return roles
	}
	return defaultRolesPerPlatform[PlatformGeneric]
}

func generateOperatorServiceMonitor(service *v1.Service, namespace string) *monitoring.ServiceMonitor {
	serviceMonitor := metrics.GenerateServiceMonitor(service)
	for i := range serviceMonitor.Spec.Endpoints {
		if serviceMonitor.Spec.Endpoints[i].Port == ctrlMetrics.ControllerMetricsServiceName {
			serviceMonitor.Spec.Endpoints[i].Path = ctrlMetrics.HandlerPath
			serviceMonitor.Spec.Endpoints[i].Scheme = "https"
			serviceMonitor.Spec.Endpoints[i].BearerTokenFile = serviceMonitorBearerTokenFile
			serviceMonitor.Spec.Endpoints[i].TLSConfig = &monitoring.TLSConfig{
				CAFile:     serviceMonitorTLSCAFile,
				ServerName: "metrics." + namespace + ".svc",
			}
		}
	}
	return serviceMonitor
}

// createOrUpdateServiceMonitor creates or updates the ServiceMonitor if it already exists.
func createOrUpdateServiceMonitor(ctx context.Context, mClient *monclientv1.MonitoringV1Client,
	namespace string, serviceMonitor *monitoring.ServiceMonitor) error {
	_, err := mClient.ServiceMonitors(namespace).Create(ctx, serviceMonitor, metav1.CreateOptions{})
	if err != nil && !kerr.IsAlreadyExists(err) {
		return err
	}
	if kerr.IsAlreadyExists(err) {
		currentServiceMonitor, getErr := mClient.ServiceMonitors(namespace).Get(ctx, serviceMonitor.Name,
			metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		serviceMonitorCopy := currentServiceMonitor.DeepCopy()
		serviceMonitorCopy.Spec = serviceMonitor.Spec
		if _, updateErr := mClient.ServiceMonitors(namespace).Update(ctx, serviceMonitorCopy,
			metav1.UpdateOptions{}); updateErr != nil {
			return updateErr
		}
	}
	return nil
}

// handleServiceMonitor attempts to create a ServiceMonitor out of service, and updates it to include the controller
// metrics paths.
func handleServiceMonitor(ctx context.Context, cfg *rest.Config, mClient *monclientv1.MonitoringV1Client,
	namespace string, service *v1.Service) error {
	ok, err := k8sutil.ResourceExists(discovery.NewDiscoveryClientForConfigOrDie(cfg),
		"monitoring.coreos.com/v1", "ServiceMonitor")
	if err != nil {
		return err
	}
	if !ok {
		log.Info("Install prometheus-operator in your cluster to create ServiceMonitor objects")
		return nil
	}

	serviceMonitor := generateOperatorServiceMonitor(service, namespace)

	return createOrUpdateServiceMonitor(ctx, mClient, namespace, serviceMonitor)
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

// createNonComplianceAlert tries to create the default PrometheusRule. Returns nil.
func createNonComplianceAlert(ctx context.Context, client *monclientv1.MonitoringV1Client, namespace string) error {
	rule := monitoring.Rule{
		Alert: "NonCompliant",
		Expr:  intstr.FromString(`compliance_operator_compliance_state{name=~".+"} > 0`),
		For:   "1s",
		Labels: map[string]string{
			"severity": "warning",
		},
		Annotations: map[string]string{
			"summary":     "The cluster is out-of-compliance",
			"description": "The compliance suite {{ $labels.name }} returned as NON-COMPLIANT, ERROR, or INCONSISTENT",
		},
	}
	_, createErr := client.PrometheusRules(namespace).Create(ctx, &monitoring.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      alertName,
		},
		Spec: monitoring.PrometheusRuleSpec{
			Groups: []monitoring.RuleGroup{
				{
					Name: "compliance",
					Rules: []monitoring.Rule{
						rule,
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	if createErr != nil && !kerr.IsAlreadyExists(createErr) {
		log.Info("could not create prometheus rule for alert", createErr)
	}
	return nil
}
