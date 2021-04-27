package compliancescan

import (
	"context"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	"github.com/openshift/compliance-operator/pkg/utils"
)

const (
	// configMap that contains the default script
	OpenScapScriptConfigMapName = "openscap-container-entrypoint"
	// This is how the script would be mounted
	OpenScapScriptPath = "/scripts/openscap-container-entrypoint"

	// a configMap with env vars for the script
	OpenScapEnvConfigMapName = "openscap-env-map"
	// A configMap same as above but minus hostroot
	OpenScapPlatformEnvConfigMapName = "openscap-env-map-platform"

	// environment variables the default script consumes
	OpenScapHostRootEnvName     = "HOSTROOT"
	OpenScapProfileEnvName      = "PROFILE"
	OpenScapContentEnvName      = "CONTENT"
	OpenScapReportDirEnvName    = "REPORT_DIR"
	OpenScapRuleEnvName         = "RULE"
	OpenScapVerbosityeEnvName   = "VERBOSITY"
	OpenScapTailoringDirEnvName = "TAILORING_DIR"
	HTTPSProxyEnvName           = "HTTPS_PROXY"
	DisconnectedInstallEnvName  = "DISCONNECTED"

	ResultServerPort = int32(8443)

	// Tailoring constants
	OpenScapTailoringDir = "/tailoring"

	PlatformScanName                  = "api-checks"
	PlatformScanResourceCollectorName = "api-resource-collector"
	// This coincides with the default ocp_data_root var in CaC.
	PlatformScanDataRoot = "/kubernetes-api-resources"
)

var defaultOpenScapScriptContents = `#!/bin/bash

ARF_REPORT=/tmp/report-arf.xml

if [ -z $PROFILE ]; then
    echo "profile not set"
    exit 1
fi

if [ -z $CONTENT ]; then
    echo "content not set"
    exit 1
fi

if [ -z $REPORT_DIR ]; then
    echo "report_dir not set"
    exit 1
fi

if [ -f "$REPORT_DIR/exit_code" ]; then
	echo "$REPORT_DIR/exit_code file found. Scan had already been run before."
	exit 0
fi

if [ -z $HOSTROOT ]; then
	echo "HOSTROOT not set, using normal oscap"
	cmd=(
		oscap xccdf eval \
	)
else
	cmd=(
		oscap-chroot $HOSTROOT xccdf eval \
	)
fi

if [ ! -z $VERBOSITY ]; then
    cmd+=(--verbose $VERBOSITY)
fi

if [ ! -z "$TAILORING_DIR" ]; then
	cmd+=(--tailoring-file "$TAILORING_DIR/tailoring.xml")
fi

if [ ! -z "$HTTPS_PROXY" ]; then
	export http_proxy="$HTTPS_PROXY"
fi

if [ -z "$DISCONNECTED" ]; then
	cmd+=(--fetch-remote-resources)
fi

cmd+=(
    --profile $PROFILE \
    --results-arf $ARF_REPORT
)

if [ ! -z $RULE ]; then
    cmd+=(--rule $RULE)
fi

cmd+=($CONTENT)

# The whole purpose of the shell entrypoint is to semi-atomically
# move the results file when the command is done so the log collector
# picks up the whole thing and not a partial file
echo "Running oscap-chroot $(rpm -q openscap-scanner) as ${cmd[@]}"
"${cmd[@]}" &> $REPORT_DIR/cmd_output 
rv=$?
echo "The scanner returned $rv"
cat $REPORT_DIR/cmd_output 

# Split the ARF so that we can only process the results
SPLIT_DIR=/tmp/split
# The XCCDF result file has a well-known name report.xml under the
# split directory
XCCDF_PATH=$SPLIT_DIR/report.xml

# The rds-split command splits the ARF into the DS part and the
# XCCDF result. At the moment we need to use the --skip-valid option
# because at the moment oscap-chroot uses chroot://host as the hostname
# and that makes the rds-split command puke.
oscap ds rds-split --skip-valid $ARF_REPORT $SPLIT_DIR
split_rv=$?
echo "The rds-split operation returned $split_rv"

# Put both the XCCDF result and the full ARF result into the report
# directory.
test -f $XCCDF_PATH && mv $XCCDF_PATH $REPORT_DIR
test -f $ARF_REPORT && mv $ARF_REPORT $REPORT_DIR
echo "$rv" > $REPORT_DIR/exit_code

# Return success
exit 0
`

func createConfigMaps(r *ReconcileComplianceScan, scriptCmName, envCmName, platformEnvCmName string, scan *compv1alpha1.ComplianceScan) error {
	cm := &corev1.ConfigMap{}

	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      scriptCmName,
		Namespace: common.GetComplianceOperatorNamespace(),
	}, cm); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		if err := r.client.Create(context.TODO(), defaultOpenScapScriptCm(scriptCmName, scan)); err != nil {
			return err
		}
	}

	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      envCmName,
		Namespace: common.GetComplianceOperatorNamespace(),
	}, cm); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		if err := r.client.Create(context.TODO(), defaultOpenScapEnvCm(envCmName, scan)); err != nil {
			return err
		}
	}

	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      platformEnvCmName,
		Namespace: common.GetComplianceOperatorNamespace(),
	}, cm); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		if err := r.client.Create(context.TODO(), platformOpenScapEnvCm(platformEnvCmName, scan)); err != nil {
			return err
		}
	}

	return nil
}

func defaultOpenScapScriptCm(name string, scan *compv1alpha1.ComplianceScan) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: common.GetComplianceOperatorNamespace(),
			Labels: map[string]string{
				compv1alpha1.ComplianceScanLabel: scan.Name,
				compv1alpha1.ScriptLabel:         "",
			},
		},
		Data: map[string]string{
			OpenScapScriptConfigMapName: defaultOpenScapScriptContents,
		},
	}
}

func platformOpenScapScriptCm(name string, scan *compv1alpha1.ComplianceScan) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: common.GetComplianceOperatorNamespace(),
			Labels: map[string]string{
				compv1alpha1.ComplianceScanLabel: scan.Name,
				compv1alpha1.ScriptLabel:         "",
			},
		},
		Data: map[string]string{
			OpenScapScriptConfigMapName: defaultOpenScapScriptContents,
		},
	}
}

func commonOpenScapEnvCm(name string, scan *compv1alpha1.ComplianceScan) *corev1.ConfigMap {
	content := absContentPath(scan.Spec.Content)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: common.GetComplianceOperatorNamespace(),
			Labels: map[string]string{
				compv1alpha1.ComplianceScanLabel: scan.Name,
				compv1alpha1.ScriptLabel:         "",
			},
		},
		Data: map[string]string{
			OpenScapProfileEnvName:   scan.Spec.Profile,
			OpenScapContentEnvName:   content,
			OpenScapReportDirEnvName: "/reports",
		},
	}

	if scan.Spec.Rule != "" {
		cm.Data[OpenScapRuleEnvName] = scan.Spec.Rule
	}

	if scan.Spec.Debug {
		// info seems like a good compromise in terms of verbosity
		cm.Data[OpenScapVerbosityeEnvName] = "INFO"
	}

	if scan.Spec.TailoringConfigMap != nil {
		cm.Data[OpenScapTailoringDirEnvName] = OpenScapTailoringDir
	}

	proxy := getHttpsProxy(scan)
	if proxy != "" {
		cm.Data[HTTPSProxyEnvName] = proxy
	}

	if scan.Spec.NoExternalResources {
		cm.Data[DisconnectedInstallEnvName] = "true"
	}

	return cm
}

func getHttpsProxy(scan *compv1alpha1.ComplianceScan) string {
	if scan.Spec.HTTPSProxy != "" {
		return scan.Spec.HTTPSProxy
	}

	return os.Getenv("HTTPS_PROXY")
}

func defaultOpenScapEnvCm(name string, scan *compv1alpha1.ComplianceScan) *corev1.ConfigMap {
	cm := commonOpenScapEnvCm(name, scan)
	cm.Data[OpenScapHostRootEnvName] = "/host"
	return cm
}

// Same as above but without hostroot.
func platformOpenScapEnvCm(name string, scan *compv1alpha1.ComplianceScan) *corev1.ConfigMap {
	return commonOpenScapEnvCm(name, scan)
}

func scriptCmForScan(scan *compv1alpha1.ComplianceScan) string {
	return utils.DNSLengthName("scap-entrypoint-", "%s-%s", scan.Name, OpenScapScriptConfigMapName)
}

func envCmForScan(scan *compv1alpha1.ComplianceScan) string {
	return utils.DNSLengthName("scap-env-", "%s-%s", scan.Name, OpenScapEnvConfigMapName)
}

func envCmForPlatformScan(scan *compv1alpha1.ComplianceScan) string {
	return utils.DNSLengthName("scap-env-", "%s-%s", scan.Name, OpenScapPlatformEnvConfigMapName)
}
