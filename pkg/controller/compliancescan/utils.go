package compliancescan

import (
	"context"
	"path"
	// we can suppress the gosec warning about sha1 here because we don't use sha1 for crypto
	// purposes, but only as a string shortener
	// #nosec G505
	"crypto/sha1"
	"fmt"
	"io"
	"os"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/utils"
)

const (
	DefaultContentContainerImage = "quay.io/jhrozek/ocp4-openscap-content:latest"
	CACertDataKey                = "ca.crt"
	CAKeyDataKey                 = "ca.key"
	ServerCertInstanceSuffix     = "-rs"
	ClientCertInstanceSuffix     = "-client"
	ServerCertPrefix             = "result-server-cert-"
	ClientCertPrefix             = "result-client-cert-"
	RootCAPrefix                 = "root-ca-"
	CertValidityDays             = 1
)

type ComplianceComponent uint

const (
	LOG_COLLECTOR = iota
	OPENSCAP
	RESULT_SERVER
	AGGREGATOR
)

var componentDefaults = []struct {
	defaultImage string
	envVar       string
}{
	{"quay.io/compliance-operator/resultscollector:latest", "LOG_COLLECTOR_IMAGE"},
	{"quay.io/jhrozek/openscap-ocp:latest", "OPENSCAP_IMAGE"},
	{"quay.io/compliance-operator/resultserver:latest", "RESULT_SERVER_IMAGE"},
	{"quay.io/compliance-operator/remediation-aggregator", "REMEDIATION_AGGREGATOR_IMAGE"},
}

// GetComponentImage returns a full image pull spec for a given component
// based on the component type
func GetComponentImage(component ComplianceComponent) string {
	comp := componentDefaults[component]

	imageTag := os.Getenv(comp.envVar)
	if imageTag == "" {
		imageTag = comp.defaultImage
	}
	return imageTag
}

func dnsLengthName(hashPrefix string, format string, a ...interface{}) string {
	const maxDnsLen = 64

	friendlyName := fmt.Sprintf(format, a...)
	if len(friendlyName) < maxDnsLen {
		return friendlyName
	}

	// If that's too long, just hash the name. It's not very user friendly, but whatever
	//
	// We can suppress the gosec warning about sha1 here because we don't use sha1 for crypto
	// purposes, but only as a string shortener
	// #nosec G401
	hasher := sha1.New()
	io.WriteString(hasher, friendlyName)
	return hashPrefix + fmt.Sprintf("%x", hasher.Sum(nil))
}

func absContentPath(relContentPath string) string {
	return path.Join("/content/", relContentPath)
}

// Issue a server cert using the instance Root CA (it needs to be created prior to calling this function).
func makeServerCertSecret(c client.Client, instance *compv1alpha1.ComplianceScan, namespace string) (*v1.Secret, error) {
	// Creating the server cert, first fetch the root CA.
	caSecret := &corev1.Secret{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: RootCAPrefix + instance.Name, Namespace: namespace}, caSecret)
	if err != nil {
		return nil, err
	}

	return serverCertSecret(instance, caSecret.Data[corev1.TLSCertKey], caSecret.Data[corev1.TLSPrivateKeyKey], namespace)
}

// Issue a server cert (signed by caKey) for instance and return in a secret. Separated from makeServerCertSecret() to help with testing.
func serverCertSecret(instance *compv1alpha1.ComplianceScan, ca, caKey []byte, namespace string) (*v1.Secret, error) {
	cert, key, err := utils.NewServerCert(ca, caKey, instance.Name+ServerCertInstanceSuffix, CertValidityDays)
	if err != nil {
		return nil, err
	}

	return certSecret(getServerCertSecretName(instance), namespace, cert, key, ca), nil
}

// Issue a client cert using the instance Root CA (it needs to be created prior to calling this function).
func makeClientCertSecret(c client.Client, instance *compv1alpha1.ComplianceScan, namespace string) (*v1.Secret, error) {
	// Creating the client cert, first fetch the root CA.
	caSecret := &corev1.Secret{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: RootCAPrefix + instance.Name, Namespace: namespace}, caSecret)
	if err != nil {
		return nil, err
	}

	return clientCertSecret(instance, caSecret.Data[corev1.TLSCertKey], caSecret.Data[corev1.TLSPrivateKeyKey], namespace)
}

// Issue a client cert (signed by caKey) for instance and return in a secret. Separated from makeClientCertSecret() to help with testing.
func clientCertSecret(instance *compv1alpha1.ComplianceScan, ca, caKey []byte, namespace string) (*v1.Secret, error) {
	cert, key, err := utils.NewClientCert(ca, caKey, instance.Name+ClientCertInstanceSuffix, CertValidityDays)
	if err != nil {
		return nil, err
	}

	return certSecret(getClientCertSecretName(instance), namespace, cert, key, ca), nil
}

func makeCASecret(instance *compv1alpha1.ComplianceScan, namespace string) (*v1.Secret, error) {
	cert, key, err := utils.ComplianceOperatorRootCA(RootCAPrefix+instance.Name, CertValidityDays)
	if err != nil {
		return nil, err
	}

	return certSecret(getCASecretName(instance), namespace, cert, key, []byte{}), nil
}

func getServerCertSecretName(instance *compv1alpha1.ComplianceScan) string {
	return ServerCertPrefix + instance.Name
}

func getClientCertSecretName(instance *compv1alpha1.ComplianceScan) string {
	return ClientCertPrefix + instance.Name
}

func getCASecretName(instance *compv1alpha1.ComplianceScan) string {
	return RootCAPrefix + instance.Name
}

func certSecret(name, namespace string, cert, key, ca []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       cert,
			corev1.TLSPrivateKeyKey: key,
			// The CA is also included in tls.crt, this is for convenience.
			CACertDataKey: ca,
		},
	}
}

func secretExists(c client.Client, name, namespace string) (bool, error) {
	s := &corev1.Secret{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, s)
	if err != nil && !errors.IsNotFound(err) {
		return false, err
	}
	return err == nil, nil
}
