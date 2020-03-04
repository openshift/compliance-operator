package utils

import (
	"time"

	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authentication/user"
)

func ComplianceOperatorRootCA(certname string, expire int) ([]byte, []byte, error) {
	config, err := libgocrypto.MakeSelfSignedCAConfig(certname, expire)
	if err != nil {
		return nil, nil, err
	}

	return config.GetPEMBytes()
}

func NewServerCert(caCert, caKey []byte, certname string, expire int) ([]byte, []byte, error) {
	ca, err := libgocrypto.GetCAFromBytes(caCert, caKey)
	if err != nil {
		return nil, nil, err
	}
	config, err := ca.MakeServerCert(sets.NewString(certname), expire)
	if err != nil {
		return nil, nil, err
	}

	return config.GetPEMBytes()
}

func NewClientCert(caCert, caKey []byte, certname string, expire int) ([]byte, []byte, error) {
	ca, err := libgocrypto.GetCAFromBytes(caCert, caKey)
	if err != nil {
		return nil, nil, err
	}
	config, err := ca.MakeClientCertificateForDuration(&user.DefaultInfo{Name: certname}, time.Duration(expire)*24*time.Hour)
	if err != nil {
		return nil, nil, err
	}

	return config.GetPEMBytes()
}
