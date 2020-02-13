package compliancescan

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
)

const (
	// configMap that contains the default script
	OpenScapScriptConfigMapName = "openscap-container-entrypoint"
	// This is how the script would be mounted
	OpenScapScriptPath = "/scripts/openscap-container-entrypoint"

	// a configMap with env vars for the script
	OpenScapEnvConfigMapName = "openscap-env-map"

	// environment variables the default script consumes
	OpenScapHostRootEnvName  = "HOSTROOT"
	OpenScapProfileEnvName   = "PROFILE"
	OpenScapContentEnvName   = "CONTENT"
	OpenScapReportDirEnvName = "REPORT_DIR"
	OpenScapRuleEnvName      = "RULE"

	ResultServerPort = int32(8080)
)

var defaultOpenScapScriptContents = `#!/bin/bash

if [ -z $HOSTROOT ]; then
    echo "hostroot not set"
    exit 1
fi

if [ -z $PROFILE ]; then
    echo "profile not set"
    exit 1
fi

if [ -z $CONTENT ]; then
    echo "content not set"
    exit 1
fi

if [ -z $REPORT_DIR ]; then
    echo "report_dit not set"
    exit 1
fi

cmd=(
    oscap-chroot $HOSTROOT xccdf eval \
    --fetch-remote-resources \
    --profile $PROFILE \
    --report /tmp/scan-report.xml \
    --results-arf /tmp/report.xml
    )

if [ ! -z $RULE ]; then
    cmd+=(--rule $RULE)
fi

cmd+=($CONTENT)

# The whole purpose of the shell entrypoint is to semi-atomically
# move the results file when the command is done so the log collector
# picks up the whole thing and not a partial file
echo "Running oscap-chroot as ${cmd[@]}"
"${cmd[@]}"
rv=$?
echo "The scanner returned $rv"
test -f /tmp/report.xml && mv /tmp/report.xml $REPORT_DIR
exit $rv
`

func createConfigMaps(r *ReconcileComplianceScan, scriptCmName, envCmName string, scan *complianceoperatorv1alpha1.ComplianceScan) error {
	cm := &corev1.ConfigMap{}

	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      scriptCmName,
		Namespace: scan.Namespace,
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
		Namespace: scan.Namespace,
	}, cm); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		if err := r.client.Create(context.TODO(), defaultOpenScapEnvCm(envCmName, scan)); err != nil {
			return err
		}
	}

	return nil
}

func defaultOpenScapScriptCm(name string, scan *complianceoperatorv1alpha1.ComplianceScan) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: scan.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				asOwner(scan),
			},
		},
		Data: map[string]string{
			OpenScapScriptConfigMapName: defaultOpenScapScriptContents,
		},
	}
}

func defaultOpenScapEnvCm(name string, scan *complianceoperatorv1alpha1.ComplianceScan) *corev1.ConfigMap {
	content := scan.Spec.Content
	if !strings.HasPrefix(scan.Spec.Content, "/") {
		content = "/content/" + scan.Spec.Content
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: scan.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				asOwner(scan),
			},
		},
		Data: map[string]string{
			OpenScapHostRootEnvName:  "/host",
			OpenScapProfileEnvName:   scan.Spec.Profile,
			OpenScapContentEnvName:   content,
			OpenScapReportDirEnvName: "/reports",
		},
	}

	if scan.Spec.Rule != "" {
		cm.Data[OpenScapRuleEnvName] = scan.Spec.Rule
	}

	return cm
}

func scriptCmForScan(scan *complianceoperatorv1alpha1.ComplianceScan) string {
	return dnsLengthName("scap-entrypoint-", "%s-%s", scan.Name, OpenScapScriptConfigMapName)
}

func envCmForScan(scan *complianceoperatorv1alpha1.ComplianceScan) string {
	return dnsLengthName("scap-env-", "%s-%s", scan.Name, OpenScapEnvConfigMapName)
}

func asOwner(scan *complianceoperatorv1alpha1.ComplianceScan) metav1.OwnerReference {
	bTrue := true

	return metav1.OwnerReference{
		APIVersion: scan.APIVersion,
		Kind:       scan.Kind,
		Name:       scan.Name,
		UID:        scan.UID,
		Controller: &bTrue,
	}
}
