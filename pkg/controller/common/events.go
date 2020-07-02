package common

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
