package common

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

type SafeRecorder struct {
	recorder record.EventRecorder
}

func NewSafeRecorder(name string, mgr manager.Manager) *SafeRecorder {
	return &SafeRecorder{recorder: mgr.GetEventRecorderFor(name)}

}

func (sr *SafeRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	if sr.recorder == nil {
		return
	}

	sr.recorder.Event(object, eventtype, reason, message)
}

// Eventf is just like Event, but with Sprintf for the message field.
func (sr *SafeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	if sr.recorder == nil {
		return
	}

	sr.recorder.Eventf(object, eventtype, reason, messageFmt, args...)
}

// AnnotatedEventf is just like eventf, but with annotations attached
func (sr *SafeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	if sr.recorder == nil {
		return
	}

	sr.recorder.AnnotatedEventf(object, annotations, eventtype, reason, messageFmt, args...)
}

func GenerateEventForResult(recorder record.EventRecorder, obj runtime.Object, objInfo metav1.Object, result compv1alpha1.ComplianceScanStatusResult) {
	// Event for Suite
	recorder.Event(
		obj,
		corev1.EventTypeNormal,
		"ResultAvailable",
		fmt.Sprintf("The result is: %s", result),
	)

	ownerRefs := objInfo.GetOwnerReferences()
	if len(ownerRefs) == 0 {
		return //there is nothing to do, since no owner is set
	}
	for idx := range ownerRefs {
		ownerRef := &ownerRefs[idx]
		// we are making an assumption that the GRC policy has a single owner, or we chose the first owner in the list
		if string(ownerRef.UID) == "" {
			continue //there is nothing to do, since no owner UID is set
		}
		// FIXME(jaosorior): Figure out a less hacky way to check this
		if ownerRef.Kind == "Policy" {
			pol := getParentPolicy(ownerRef, objInfo.GetNamespace())
			recorder.Event(
				pol,
				corev1.EventTypeNormal,
				fmt.Sprintf("policy: %s/%s", objInfo.GetNamespace(), objInfo.GetName()),
				resultToACMPolicyStatus(objInfo.GetNamespace(), objInfo.GetName(), result),
			)
		}
	}
}

func getParentPolicy(ownerRef *metav1.OwnerReference, ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": ownerRef.APIVersion,
			"kind":       ownerRef.Kind,
			"metadata": map[string]interface{}{
				"name":      ownerRef.Name,
				"namespace": ns,
				"uid":       ownerRef.UID,
			},
		},
	}
}

func resultToACMPolicyStatus(namespace, name string, scanresult compv1alpha1.ComplianceScanStatusResult) string {
	const instfmt string = "; To view aggregated results, execute the following in the managed cluster: kubectl get compliancesuites -n %s %s"
	instructions := fmt.Sprintf(instfmt, namespace, name)
	var result string
	switch scanresult {
	case compv1alpha1.ResultCompliant:
		result = "Compliant"
	case compv1alpha1.ResultNonCompliant:
		result = "NonCompliant"
	default:
		result = "UnknownCompliancy"
	}
	return result + instructions
}
