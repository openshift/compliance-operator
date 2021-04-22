module github.com/openshift/compliance-operator

go 1.14

require (
	github.com/ajeddeloh/go-json v0.0.0-20200220154158-5ae607161559 // indirect
	github.com/antchfx/xmlquery v1.3.6
	github.com/antchfx/xpath v1.1.11 // indirect
	github.com/cenkalti/backoff/v3 v3.2.2
	github.com/clarketm/json v1.15.7
	github.com/coreos/ignition/v2 v2.9.0
	github.com/dsnet/compress v0.0.1
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/go-logr/logr v0.4.0
	github.com/go-logr/zapr v0.4.0
	github.com/google/go-cmp v0.5.5
	github.com/gorilla/mux v1.8.0 // indirect
	github.com/nbutton23/zxcvbn-go v0.0.0-20210217022336-fa2cb2858354 // indirect
	github.com/onsi/ginkgo v1.16.1
	github.com/onsi/gomega v1.11.0
	github.com/openshift/api v0.0.0-20200829102639-8a3a835f1acf
	github.com/openshift/library-go v0.0.0-20200831114015-2ab0c61c15de
	github.com/openshift/machine-config-operator v0.0.1-0.20200913004441-7eba765c69c9
	github.com/operator-framework/operator-sdk v0.19.4
	github.com/robfig/cron/v3 v3.0.1
	github.com/securego/gosec v0.0.0-20200302134848-c998389da2ac
	github.com/sergi/go-diff v1.1.0 // indirect
	github.com/sirupsen/logrus v1.7.0 // indirect
	github.com/spf13/cobra v1.1.1
	github.com/subchen/go-xmldom v1.1.2
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50 // indirect
	go.uber.org/zap v1.14.1
	k8s.io/api v0.19.10
	k8s.io/apimachinery v0.19.10
	k8s.io/apiserver v0.19.10
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.6.2
)

replace (
	github.com/openshift/api => github.com/openshift/api v0.0.0-20200829102639-8a3a835f1acf
	github.com/openshift/machine-config-operator => github.com/openshift/machine-config-operator v0.0.1-0.20200913004441-7eba765c69c9
	k8s.io/api => k8s.io/api v0.19.10
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.19.10
	k8s.io/apimachinery => k8s.io/apimachinery v0.19.10
	k8s.io/apiserver => k8s.io/apiserver v0.19.10
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.19.10
	k8s.io/client-go => k8s.io/client-go v0.19.10
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.19.10
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.19.10
	k8s.io/code-generator => k8s.io/code-generator v0.19.10
	k8s.io/component-base => k8s.io/component-base v0.19.10
	k8s.io/cri-api => k8s.io/cri-api v0.19.10
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.19.10
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.19.10
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.19.10
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.19.10
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.19.10
	k8s.io/kubectl => k8s.io/kubectl v0.19.10
	k8s.io/kubelet => k8s.io/kubelet v0.19.10
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.19.10
	k8s.io/metrics => k8s.io/metrics v0.19.10
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.19.10
)

replace github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible

replace github.com/docker/docker => github.com/moby/moby v0.7.3-0.20190826074503-38ab9da00309 // Required by Helm

replace github.com/gorilla/websocket => github.com/gorilla/websocket v1.4.2
