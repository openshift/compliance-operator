package metrics

import (
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

const (
	metricNamespace = "compliance_operator"

	metricNameComplianceScanStatus        = "compliance_scan_status_total"
	metricNameComplianceScanError         = "compliance_scan_error_total"
	metricNameComplianceRemediationStatus = "compliance_remediation_status_total"

	metricLabelScanResult       = "result"
	metricLabelScanName         = "name"
	metricLabelScanPhase        = "phase"
	metricLabelScanError        = "error"
	metricLabelRemediationName  = "name"
	metricLabelRemediationState = "state"

	HandlerPath                  = "/metrics-co"
	ControllerMetricsServiceName = "metrics-co"
	MetricsAddrListen            = ":8585"
)

// Metrics is the main structure of this package.
type Metrics struct {
	impl    impl
	log     logr.Logger
	metrics *ControllerMetrics
}

type ControllerMetrics struct {
	metricComplianceScanError         *prometheus.CounterVec
	metricComplianceScanStatus        *prometheus.CounterVec
	metricComplianceRemediationStatus *prometheus.CounterVec
}

func DefaultControllerMetrics() *ControllerMetrics {
	return &ControllerMetrics{
		metricComplianceScanError: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:      metricNameComplianceScanError,
				Namespace: metricNamespace,
				Help:      "A counter for the total number of encounters of error",
			},
			[]string{metricLabelScanName, metricLabelScanError},
		),
		metricComplianceScanStatus: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:      metricNameComplianceScanStatus,
				Namespace: metricNamespace,
				Help:      "A counter for the total number of updates to the status of a ComplianceScan",
			},
			[]string{
				metricLabelScanName,
				metricLabelScanPhase,
				metricLabelScanResult,
			},
		),
		metricComplianceRemediationStatus: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:      metricNameComplianceRemediationStatus,
				Namespace: metricNamespace,
				Help:      "A counter for the total number of updates to the status of a ComplianceRemediation",
			},
			[]string{
				metricLabelRemediationName,
				metricLabelRemediationState,
			},
		),
	}
}

func NewMetrics(imp impl) *Metrics {
	return &Metrics{
		impl:    imp,
		log:     ctrllog.Log.WithName("metrics"),
		metrics: DefaultControllerMetrics(),
	}
}

// New returns a new default Metrics instance.
func New() *Metrics {
	return NewMetrics(&defaultImpl{})
}

// Register iterates over all available metrics and registers them.
func (m *Metrics) Register() error {
	for name, collector := range map[string]prometheus.Collector{
		metricNameComplianceScanError:         m.metrics.metricComplianceScanError,
		metricNameComplianceScanStatus:        m.metrics.metricComplianceScanStatus,
		metricNameComplianceRemediationStatus: m.metrics.metricComplianceRemediationStatus,
	} {
		m.log.Info(fmt.Sprintf("Registering metric: %s", name))
		if err := m.impl.Register(collector); err != nil {
			return errors.Wrapf(err, "register collector for %s metric", name)
		}
	}
	return nil
}

func (m *Metrics) Start(s <-chan struct{}) error {
	m.log.Info("Starting to serve controller metrics")
	http.Handle(HandlerPath, promhttp.Handler())

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	tlsConfig = libgocrypto.SecureTLSConfig(tlsConfig)
	server := &http.Server{
		Addr:      MetricsAddrListen,
		TLSConfig: tlsConfig,
	}

	err := server.ListenAndServeTLS("/var/run/secrets/serving-cert/tls.crt", "/var/run/secrets/serving-cert/tls.key")
	if err != nil {
		// unhandled on purpose, we don't want to exit the operator.
		m.log.Error(err, "Metrics service failed")
	}
	<-s
	return nil
}

// IncComplianceScanStatus also increments error if necessary
func (m *Metrics) IncComplianceScanStatus(name string, status v1alpha1.ComplianceScanStatus) {
	m.metrics.metricComplianceScanStatus.With(prometheus.Labels{
		metricLabelScanName:   name,
		metricLabelScanPhase:  string(status.Phase),
		metricLabelScanResult: string(status.Result),
	}).Inc()
	if len(status.ErrorMessage) > 0 {
		m.metrics.metricComplianceScanError.With(prometheus.Labels{
			metricLabelScanName:  name,
			metricLabelScanError: status.ErrorMessage,
		}).Inc()
	}
}

// IncComplianceRemediationStatus increments the ComplianceRemediation status counter
func (m *Metrics) IncComplianceRemediationStatus(name string, status v1alpha1.ComplianceRemediationStatus) {
	m.metrics.metricComplianceRemediationStatus.With(prometheus.Labels{
		metricLabelRemediationName:  name,
		metricLabelRemediationState: string(status.ApplicationState),
	}).Inc()
}
