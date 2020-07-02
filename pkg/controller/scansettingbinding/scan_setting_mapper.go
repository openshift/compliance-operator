package scansettingbinding

import (
	"context"
	"github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type scanSettingMapper struct {
	client.Client
}

func (s *scanSettingMapper) Map(obj handler.MapObject) []reconcile.Request {
	var requests []reconcile.Request

	ssbList := v1alpha1.ScanSettingBindingList{}
	err := s.List(context.TODO(), &ssbList, &client.ListOptions{})
	if err != nil {
		return requests
	}

	for _, ssb := range ssbList.Items {
		if ssb.SettingsRef == nil {
			continue
		}

		if ssb.SettingsRef.Name != obj.Meta.GetName() {
			continue
		}

		objKey := types.NamespacedName{
			Name:      ssb.GetName(),
			Namespace: ssb.GetNamespace(),
		}
		requests = append(requests, reconcile.Request{NamespacedName: objKey})
	}

	return requests
}
