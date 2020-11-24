package utils

import (
	"encoding/base64"
	"io"
	"io/ioutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
)

func encodetoBase64(src io.Reader) string {
	pr, pw := io.Pipe()
	enc := base64.NewEncoder(base64.StdEncoding, pw)
	go func() {
		_, err := io.Copy(enc, src)
		enc.Close()

		if err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()
	out, _ := ioutil.ReadAll(pr)
	return string(out)
}

// GetResultConfigMap gets a configmap that reflects a result or an error for a scan
func GetResultConfigMap(owner metav1.Object, configMapName, filename, nodeName string, contents io.Reader, compressed bool, exitcode string, warnings string) *corev1.ConfigMap {
	var strcontents string
	annotations := map[string]string{}
	if compressed {
		annotations = map[string]string{
			"openscap-scan-result/compressed": "",
		}
		strcontents = encodetoBase64(contents)
	} else {
		contentBytes, _ := ioutil.ReadAll(contents)
		strcontents = string(contentBytes)
	}
	if nodeName != "" {
		annotations["openscap-scan-result/node"] = nodeName
	}

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        configMapName,
			Namespace:   common.GetComplianceOperatorNamespace(),
			Annotations: annotations,
			Labels: map[string]string{
				compv1alpha1.ComplianceScanLabel: owner.GetName(),
				compv1alpha1.ResultLabel:         "",
			},
		},
		Data: map[string]string{
			"exit-code": exitcode,
			filename:    strcontents,
			"warnings":  warnings,
		},
	}
}
