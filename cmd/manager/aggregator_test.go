package main

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("Aggregator Empty Remediation Test", func() {

	var (
		namespace          = "test-ns"
		remediationName    = "testRem"
		targetNodeSelector = map[string]string{
			"hops": "malt",
		}
	)
	nodeScanSettings := compv1alpha1.ComplianceScanSpec{
		ScanType:     compv1alpha1.ScanTypeNode,
		NodeSelector: targetNodeSelector,
	}

	nodeScan := &compv1alpha1.ComplianceScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testScanNode",
			Namespace: namespace,
		},
		Spec: nodeScanSettings,
	}
	emptyRem := &compv1alpha1.ComplianceRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remediationName,
			Namespace: namespace,
		},
		Spec: compv1alpha1.ComplianceRemediationSpec{
			ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
				Apply: true,
			},
			Current: compv1alpha1.ComplianceRemediationPayload{
				Object: &unstructured.Unstructured{
					Object: map[string]interface{}{},
				},
			},
		},
	}
	canCreate, _ := canCreateRemediationObject(nodeScan, emptyRem.Spec.Current.Object)
	Expect(canCreate).To(BeFalse())
})
