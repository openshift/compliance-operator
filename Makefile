# Operator variables
# ==================
export APP_NAME=compliance-operator
RELATED_IMAGE_OPENSCAP_NAME=openscap-ocp

# Container image variables
# =========================
IMAGE_REPO?=quay.io/compliance-operator

# Detect the OS to set per-OS defaults
OS_NAME=$(shell uname -s)
# Container runtime
ifeq ($(OS_NAME), Linux)
    RUNTIME?=podman
    SED=sed -i
else ifeq ($(OS_NAME), Darwin)
    RUNTIME?=docker
    SED=sed -i ''
endif

ARCH?=$(shell uname -m)
ifeq ($(ARCH), x86_64)
    OPM_ARCH?=amd64
else
    OPM_ARCH?=$(ARCH)
endif

ifeq ($(RUNTIME), podman)
    LOGIN_PUSH_OPTS="--tls-verify=false"
else ifeq ($(RUNTIME), docker)
    LOGIN_PUSH_OPTS=
endif

# Temporary
RELATED_IMAGE_OPENSCAP_TAG?=1.3.5

# Image path to use. Set this if you want to use a specific path for building
# or your e2e tests. This is overwritten if we bulid the image and push it to
# the cluster or if we're on CI.
DEFAULT_IMAGE_OPERATOR_PATH=quay.io/compliance-operator/$(APP_NAME):latest
DEFAULT_IMAGE_OPENSCAP_PATH=quay.io/compliance-operator/$(RELATED_IMAGE_OPENSCAP_NAME):$(RELATED_IMAGE_OPENSCAP_TAG)
RELATED_IMAGE_OPERATOR_PATH?=$(IMAGE_REPO)/$(APP_NAME)
RELATED_IMAGE_OPENSCAP_PATH?=$(IMAGE_REPO)/$(RELATED_IMAGE_OPENSCAP_NAME)
OPENSCAP_DOCKER_CONTEXT=./images/openscap

# Image tag to use. Set this if you want to use a specific tag for building
# or your e2e tests.
TAG?=latest

BUNDLE_IMAGE_NAME=compliance-operator-bundle
BUNDLE_IMAGE_PATH=$(IMAGE_REPO)/$(BUNDLE_IMAGE_NAME)
BUNDLE_IMAGE_TAG?=$(TAG)
TEST_BUNDLE_IMAGE_TAG?=testonly
INDEX_IMAGE_NAME=compliance-operator-index
INDEX_IMAGE_PATH=$(IMAGE_REPO)/$(INDEX_IMAGE_NAME)
INDEX_IMAGE_TAG?=latest


# Build variables
# ===============
CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/build/_output
GOFLAGS?=-mod=vendor
GO=GOFLAGS=$(GOFLAGS) GO111MODULE=auto go
GOBUILD=$(GO) build
BUILD_GOPATH=$(TARGET_DIR):$(CURPATH)/cmd
TARGET=$(TARGET_DIR)/bin/$(APP_NAME)
MAIN_PKG=cmd/manager/main.go
PKGS=$(shell go list ./... | grep -v -E '/vendor/|/test|/examples')
# This is currently hardcoded to our most performance sensitive package
BENCHMARK_PKG?=github.com/openshift/compliance-operator/pkg/utils

# go source files, ignore vendor directory
SRC = $(shell find . -type f -name '*.go' -not -path "./vendor/*" -not -path "./_output/*")


# Kubernetes variables
# ====================
KUBECONFIG?=$(HOME)/.kube/config
export NAMESPACE?=openshift-compliance
export OPERATOR_NAMESPACE?=openshift-compliance

# Operator-sdk variables
# ======================
SDK_VERSION?=v0.18.2
ifeq ($(OS_NAME), Linux)
    OPERATOR_SDK_URL=https://github.com/operator-framework/operator-sdk/releases/download/$(SDK_VERSION)/operator-sdk-$(SDK_VERSION)-$(ARCH)-linux-gnu
else ifeq ($(OS_NAME), Darwin)
    OPERATOR_SDK_URL=https://github.com/operator-framework/operator-sdk/releases/download/$(SDK_VERSION)/operator-sdk-$(SDK_VERSION)-x86_64-apple-darwin
endif

OPM_VERSION=v1.20.0
ifeq ($(OS_NAME), Linux)
    OPM_URL=https://github.com/operator-framework/operator-registry/releases/download/$(OPM_VERSION)/linux-$(OPM_ARCH)-opm
else ifeq ($(OS_NAME), Darwin)
    OPM_URL=https://github.com/operator-framework/operator-registry/releases/download/$(OPM_VERSION)/darwin-amd64-opm
endif

# Test variables
# ==============
TEST_OPTIONS?=
# Skip pushing the container to your cluster
E2E_SKIP_CONTAINER_PUSH?=false
# Use default images in the e2e test run. Note that this takes precedence over E2E_SKIP_CONTAINER_PUSH
E2E_USE_DEFAULT_IMAGES?=false
# In a local-env e2e run, push images to the cluster but skip building them. Useful if the container push fails.
E2E_SKIP_CONTAINER_BUILD?=false

# Pass extra flags to the e2e test run.
# e.g. to run a specific test in the e2e test suite, do:
# 	make e2e E2E_GO_TEST_FLAGS="-v -run TestE2E/TestScanWithNodeSelectorFiltersCorrectly"
E2E_GO_TEST_FLAGS?=-test.v -test.timeout 120m

# Specifies the image path to use for the content in the tests
DEFAULT_CONTENT_IMAGE_PATH=quay.io/complianceascode/ocp4:latest
E2E_CONTENT_IMAGE_PATH?=quay.io/complianceascode/ocp4:latest
# We specifically omit the tag here since we use this for testing
# different images referenced by different tags.
E2E_BROKEN_CONTENT_IMAGE_PATH?=quay.io/compliance-operator/test-broken-content

QUAY_NAMESPACE=compliance-operator
OPERATOR_VERSION?=
PREVIOUS_OPERATOR_VERSION=$(shell grep -E "\s+version: [0-9]+.[0-9]+.[0-9]+" deploy/olm-catalog/compliance-operator/manifests/compliance-operator.clusterserviceversion.yaml | sed 's/.*version: //')
PACKAGE_CHANNEL?=alpha

MUST_GATHER_IMAGE_PATH?=quay.io/compliance-operator/must-gather
MUST_GATHER_IMAGE_TAG?=latest

.PHONY: all
all: build ## Test and Build the compliance-operator

.PHONY: help
help: ## Show this help screen
	@echo 'Usage: make <OPTIONS> ... <TARGETS>'
	@echo ''
	@echo 'Available targets are:'
	@echo ''
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)


.PHONY: image
image: fmt operator-sdk operator-image bundle-image ## Build the compliance-operator container image

.PHONY: operator-image
operator-image:
	$(RUNTIME) build -t $(RELATED_IMAGE_OPERATOR_PATH):$(TAG) -f build/Dockerfile .

.PHONY: openscap-image
openscap-image:
	$(RUNTIME) build --no-cache -t $(RELATED_IMAGE_OPENSCAP_PATH):$(RELATED_IMAGE_OPENSCAP_TAG) $(OPENSCAP_DOCKER_CONTEXT)

.PHONY: bundle-image
bundle-image:
	$(RUNTIME) build -t $(BUNDLE_IMAGE_PATH):$(BUNDLE_IMAGE_TAG) -f bundle.Dockerfile .

.PHONY: test-bundle-image
test-bundle-image:
	$(RUNTIME) build -t $(BUNDLE_IMAGE_PATH):$(TEST_BUNDLE_IMAGE_TAG) -f bundle.Dockerfile .

.PHONY: index-image
index-image: opm
	$(GOPATH)/bin/opm index add -b $(BUNDLE_IMAGE_PATH):$(BUNDLE_IMAGE_TAG) -f $(INDEX_IMAGE_PATH):$(INDEX_IMAGE_TAG) -t $(INDEX_IMAGE_PATH):$(INDEX_IMAGE_TAG) -c $(RUNTIME) --overwrite-latest

.PHONY: test-index-image
test-index-image: opm test-bundle-image push-test-bundle
	$(GOPATH)/bin/opm index add -b $(BUNDLE_IMAGE_PATH):$(TEST_BUNDLE_IMAGE_TAG) -t $(INDEX_IMAGE_PATH):$(INDEX_IMAGE_TAG) -c $(RUNTIME)

.PHONY: index-image-to-cluster
index-image-to-cluster: namespace openshift-user test-index-image
	@echo "Temporarily exposing the default route to the image registry"
	@oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":true}}' --type=merge
	@echo "Pushing image $(INDEX_IMAGE_PATH):$(INDEX_IMAGE_TAG) to the image registry"
	IMAGE_REGISTRY_HOST=$$(oc get route default-route -n openshift-image-registry --template='{{ .spec.host }}'); \
		$(RUNTIME) login $(LOGIN_PUSH_OPTS) -u $(OPENSHIFT_USER) -p $(shell oc whoami -t) $${IMAGE_REGISTRY_HOST}; \
		$(RUNTIME) push $(LOGIN_PUSH_OPTS) $(INDEX_IMAGE_PATH):$(INDEX_IMAGE_TAG) $${IMAGE_REGISTRY_HOST}/openshift/$(INDEX_IMAGE_NAME):$(INDEX_IMAGE_TAG)
	@echo "Removing the route from the image registry"
	@oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":false}}' --type=merge
	$(eval LOCAL_INDEX_IMAGE_PATH = image-registry.openshift-image-registry.svc:5000/openshift/$(INDEX_IMAGE_NAME):$(INDEX_IMAGE_TAG))

.PHONY: bundle-image-to-cluster
bundle-image-to-cluster: openshift-user bundle-image
	@echo "Temporarily exposing the default route to the image registry"
	@oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":true}}' --type=merge
	@echo "Pushing image $(BUNDLE_IMAGE_PATH):$(BUNDLE_IMAGE_TAG) to the image registry"
	IMAGE_REGISTRY_HOST=$$(oc get route default-route -n openshift-image-registry --template='{{ .spec.host }}'); \
		$(RUNTIME) login $(LOGIN_PUSH_OPTS) -u $(OPENSHIFT_USER) -p $(shell oc whoami -t) $${IMAGE_REGISTRY_HOST}; \
		$(RUNTIME) push $(LOGIN_PUSH_OPTS) $(BUNDLE_IMAGE_PATH):$(BUNDLE_IMAGE_TAG) $${IMAGE_REGISTRY_HOST}/openshift/$(BUNDLE_IMAGE_NAME):$(BUNDLE_IMAGE_TAG)
	@echo "Removing the route from the image registry"
	@oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":false}}' --type=merge
	$(eval LOCAL_BUNDLE_IMAGE_PATH = image-registry.openshift-image-registry.svc:5000/openshift/$(BUNDLE_IMAGE_NAME):$(BUNDLE_IMAGE_TAG))

.PHONY: test-catalog
test-catalog: index-image-to-cluster
	@echo "WARNING: This will temporarily modify deploy/olm-catalog/catalog-source.yaml"
	@echo "Replacing image reference in deploy/olm-catalog/catalog-source.yaml"
	@$(SED) 's%quay.io/compliance-operator/compliance-operator-index:latest%$(LOCAL_INDEX_IMAGE_PATH)%' deploy/olm-catalog/catalog-source.yaml
	@oc apply -f deploy/olm-catalog/catalog-source.yaml
	@echo "Restoring image reference in deploy/olm-catalog/catalog-source.yaml"
	@$(SED) 's%$(LOCAL_INDEX_IMAGE_PATH)%quay.io/compliance-operator/compliance-operator-index:latest%' deploy/olm-catalog/catalog-source.yaml
	@oc apply -f deploy/olm-catalog/operator-group.yaml
	@oc apply -f deploy/olm-catalog/subscription.yaml

.PHONY: e2e-content-images
e2e-content-images:
	RUNTIME=$(RUNTIME) images/testcontent/broken-content.sh build ${E2E_BROKEN_CONTENT_IMAGE_PATH}

.PHONY: push-e2e-content
push-e2e-content: e2e-content-images
	RUNTIME=$(RUNTIME) images/testcontent/broken-content.sh push ${E2E_BROKEN_CONTENT_IMAGE_PATH}

.PHONY: must-gather-image
must-gather-image:
	$(RUNTIME) build -t $(MUST_GATHER_IMAGE_PATH):$(MUST_GATHER_IMAGE_TAG) -f images/must-gather/Dockerfile .

.PHONY: must-gather
must-gather: must-gather-image must-gather-push

.PHONY: build
build: fmt manager ## Build the compliance-operator binary

manager:
	$(GO) build -o $(TARGET) github.com/openshift/compliance-operator/cmd/manager

.PHONY: operator-sdk
operator-sdk: $(GOPATH)/bin/operator-sdk

$(GOPATH)/bin/operator-sdk:
	wget -nv $(OPERATOR_SDK_URL) -O $(GOPATH)/bin/operator-sdk || (echo "wget returned $$? trying to fetch operator-sdk. please install operator-sdk and try again"; exit 1)
	chmod +x $(GOPATH)/bin/operator-sdk

.PHONY: opm
opm: $(GOPATH)/bin/opm

$(GOPATH)/bin/opm:
	wget -nv $(OPM_URL) -O $(GOPATH)/bin/opm || (echo "wget returned $$? trying to fetch opm. please install opm and try again"; exit 1)
	chmod +x $(GOPATH)/bin/opm

.PHONY: run
run: operator-sdk ## Run the compliance-operator locally
	WATCH_NAMESPACE=$(NAMESPACE) \
	KUBERNETES_CONFIG=$(KUBECONFIG) \
	OPERATOR_NAME=compliance-operator \
	$(GOPATH)/bin/operator-sdk run --local --watch-namespace $(NAMESPACE) --operator-flags operator

.PHONY: clean
clean: clean-modcache clean-cache clean-output ## Clean the golang environment

.PHONY: clean-output
clean-output:
	rm -rf $(TARGET_DIR)

.PHONY: clean-cache
clean-cache:
	$(GO) clean -cache -testcache $(PKGS)

.PHONY: clean-modcache
clean-modcache:
	$(GO) clean -modcache $(PKGS)

.PHONY: fmt
fmt:  ## Run the `go fmt` tool
	@$(GO) fmt $(PKGS)

.PHONY: simplify
simplify:
	@gofmt -s -l -w $(SRC)

.PHONY: verify
verify: vet mod-verify gosec ## Run code lint checks

.PHONY: vet
vet:
	@$(GO) vet $(PKGS)

.PHONY: mod-verify
mod-verify:
	@$(GO) mod verify

.PHONY: gosec
gosec:
	@$(GO) run github.com/securego/gosec/v2/cmd/gosec -severity medium -confidence medium -quiet ./...

.PHONY: generate
generate: operator-sdk ## Run operator-sdk's code generation (k8s and crds)
	$(GOPATH)/bin/operator-sdk generate k8s
	$(GOPATH)/bin/operator-sdk generate crds
	## Make sure we update the Helm chart with the most recent CRDs
	cp deploy/crds/*crd.yaml deploy/compliance-operator-chart/crds/

.PHONY: test-unit
test-unit: fmt ## Run the unit tests
ifndef JUNITFILE
	@$(GO) test $(TEST_OPTIONS) $(PKGS)
else
	@set -o pipefail; $(GO) test $(TEST_OPTIONS) -json $(PKGS) --ginkgo.noColor | gotest2junit -v > $(JUNITFILE)
endif

.PHONY: test-benchmark
test-benchmark: ## Run the benchmark tests -- Note that this can only be ran for one package. You can set $BENCHMARK_PKG for this. cpu.prof and mem.prof will be generated
	@$(GO) test -cpuprofile cpu.prof -memprofile mem.prof -bench . $(TEST_OPTIONS) $(BENCHMARK_PKG)
	@echo "The pprof files generated are: cpu.prof and mem.prof"

# This runs the end-to-end tests. If not running this on CI, it'll try to
# push the operator image to the cluster's registry. This behavior can be
# avoided with the E2E_SKIP_CONTAINER_PUSH environment variable.
.PHONY: e2e
e2e: namespace tear-down operator-sdk image-to-cluster openshift-user deploy-crds ## Run the end-to-end tests
	@echo "WARNING: This will temporarily modify deploy/operator.yaml"
	@echo "Replacing workload references in deploy/operator.yaml"
	@$(SED) 's%$(DEFAULT_IMAGE_OPENSCAP_PATH)%$(RELATED_IMAGE_OPENSCAP_PATH)%' deploy/operator.yaml
	@$(SED) 's%$(DEFAULT_IMAGE_OPERATOR_PATH)%$(RELATED_IMAGE_OPERATOR_PATH)%' deploy/operator.yaml
	@$(SED) 's%$(DEFAULT_CONTENT_IMAGE_PATH)%$(E2E_CONTENT_IMAGE_PATH)%' deploy/operator.yaml
	@echo "Running e2e tests"
	unset GOFLAGS && BROKEN_CONTENT_IMAGE=$(E2E_BROKEN_CONTENT_IMAGE_PATH) CONTENT_IMAGE=$(E2E_CONTENT_IMAGE_PATH) $(GOPATH)/bin/operator-sdk test local ./tests/e2e --skip-cleanup-error --image "$(RELATED_IMAGE_OPERATOR_PATH)" --go-test-flags "$(E2E_GO_TEST_FLAGS)"
	@echo "Restoring image references in deploy/operator.yaml"
	@$(SED) 's%$(RELATED_IMAGE_OPENSCAP_PATH)%$(DEFAULT_IMAGE_OPENSCAP_PATH)%' deploy/operator.yaml
	@$(SED) 's%$(RELATED_IMAGE_OPERATOR_PATH)%$(DEFAULT_IMAGE_OPERATOR_PATH)%' deploy/operator.yaml
	@$(SED) 's%$(E2E_CONTENT_IMAGE_PATH)%$(DEFAULT_CONTENT_IMAGE_PATH)%' deploy/operator.yaml

e2e-local: operator-sdk tear-down deploy-crds ## Run the end-to-end tests on a locally running operator (e.g. using make run)
	@echo "WARNING: This will temporarily modify deploy/operator.yaml"
	@echo "Replacing workload references in deploy/operator.yaml"
	@$(SED) 's%$(DEFAULT_IMAGE_OPERATOR_PATH)%$(RELATED_IMAGE_OPERATOR_PATH)%' deploy/operator.yaml
	@$(SED) 's%$(DEFAULT_CONTENT_IMAGE_PATH)%$(E2E_CONTENT_IMAGE_PATH)%' deploy/operator.yaml
	unset GOFLAGS && CONTENT_IMAGE=$(E2E_CONTENT_IMAGE_PATH) $(GOPATH)/bin/operator-sdk test local ./tests/e2e --up-local --skip-cleanup-error --image "$(RELATED_IMAGE_OPERATOR_PATH)" --go-test-flags "$(E2E_GO_TEST_FLAGS)"
	@echo "Restoring image references in deploy/operator.yaml"
	@$(SED) 's%$(RELATED_IMAGE_OPERATOR_PATH)%$(DEFAULT_IMAGE_OPERATOR_PATH)%' deploy/operator.yaml
	@$(SED) 's%$(E2E_CONTENT_IMAGE_PATH)%$(DEFAULT_CONTENT_IMAGE_PATH)%' deploy/operator.yaml

# If IMAGE_FROM_CI is not defined, it means that we're not running on CI, so we
# probably want to push the compliance-operator image to the cluster we're
# developing on. This target exposes temporarily the image registry, pushes the
# image, and remove the route in the end.
#
# The IMAGE_FROM_CI variable comes from CI. It is of the format:
#     <image path in CI registry>:${component}
# Here define the `component` variable, so, when we overwrite the
# RELATED_IMAGE_OPERATOR_PATH variable, it'll expand to the component we need.
# Note that the `component` names come from the `openshift/release` repo
# config.
#
# If the E2E_SKIP_CONTAINER_PUSH environment variable is used, the target will
# assume that you've pushed images beforehand, and will merely set the
# necessary variables to use them.
#
# If the E2E_USE_DEFAULT_IMAGES environment variable is used, this will do
# nothing, and the default images will be used.
#
# If the E2E_SKIP_CONTAINER_BUILD environment variable is used, this will push
# the previously built images.
.PHONY: image-to-cluster
ifdef IMAGE_FROM_CI
image-to-cluster:
	@echo "IMAGE_FROM_CI variable detected. We're in a CI enviornment."
	@echo "We're in a CI environment, skipping image-to-cluster target."
	$(eval RELATED_IMAGE_OPERATOR_PATH = $(IMAGE_FROM_CI))
	$(eval E2E_CONTENT_IMAGE_PATH = $(CONTENT_IMAGE_FROM_CI))
	$(eval RELATED_IMAGE_OPENSCAP_PATH = $(OPENSCAP_IMAGE_FROM_CI))
else ifeq ($(E2E_USE_DEFAULT_IMAGES), true)
image-to-cluster:
	@echo "E2E_USE_DEFAULT_IMAGES variable detected. Using default images."
else ifeq ($(E2E_SKIP_CONTAINER_PUSH), true)
image-to-cluster:
	@echo "E2E_SKIP_CONTAINER_PUSH variable detected. Using previously pushed images."
	$(eval RELATED_IMAGE_OPERATOR_PATH = image-registry.openshift-image-registry.svc:5000/openshift/$(APP_NAME):$(TAG))
else ifeq ($(E2E_SKIP_CONTAINER_BUILD), true)
image-to-cluster: namespace cluster-image-push
	@echo "E2E_SKIP_CONTAINER_BUILD variable detected. Using previously built local images."
else
image-to-cluster: namespace image openscap-image cluster-image-push
	@echo "IMAGE_FROM_CI variable missing. We're in local enviornment."
endif

.PHONY: cluster-image-push
cluster-image-push: namespace openshift-user
	@echo "Temporarily exposing the default route to the image registry"
	@oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":true}}' --type=merge
	@echo "Pushing image $(RELATED_IMAGE_OPERATOR_PATH):$(TAG) to the image registry"
	IMAGE_REGISTRY_HOST=$$(oc get route default-route -n openshift-image-registry --template='{{ .spec.host }}'); \
		$(RUNTIME) login $(LOGIN_PUSH_OPTS) -u $(OPENSHIFT_USER) -p $(shell oc whoami -t) $${IMAGE_REGISTRY_HOST}; \
		$(RUNTIME) push $(LOGIN_PUSH_OPTS) $(RELATED_IMAGE_OPERATOR_PATH):$(TAG) $${IMAGE_REGISTRY_HOST}/openshift/$(APP_NAME):$(TAG); \
		$(RUNTIME) push $(LOGIN_PUSH_OPTS) $(RELATED_IMAGE_OPENSCAP_PATH):$(RELATED_IMAGE_OPENSCAP_TAG) $${IMAGE_REGISTRY_HOST}/openshift/$(RELATED_IMAGE_OPENSCAP_NAME):$(RELATED_IMAGE_OPENSCAP_TAG)
	@echo "Removing the route from the image registry"
	@oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":false}}' --type=merge
	$(eval RELATED_IMAGE_OPERATOR_PATH = image-registry.openshift-image-registry.svc:5000/openshift/$(APP_NAME):$(TAG))
	$(eval RELATED_IMAGE_OPENSCAP_PATH = image-registry.openshift-image-registry.svc:5000/openshift/$(RELATED_IMAGE_OPENSCAP_NAME):$(RELATED_IMAGE_OPENSCAP_TAG))

.PHONY: namespace
namespace:
	@echo "Creating '$(NAMESPACE)' namespace/project"
	@oc apply -f deploy/ns.yaml

.PHONY: deploy
deploy: namespace deploy-crds ## Deploy the operator from the manifests in the deploy/ directory
	@oc apply -n $(NAMESPACE) -f deploy/

.PHONY: deploy-local
deploy-local: namespace image-to-cluster deploy-crds ## Deploy the operator from the manifests in the deploy/ directory and the images from a local build
	@$(SED) 's%$(IMAGE_REPO)/$(APP_NAME):latest%$(RELATED_IMAGE_OPERATOR_PATH)%' deploy/operator.yaml
	@$(SED) 's%$(IMAGE_REPO)/$(RELATED_IMAGE_OPENSCAP_NAME):$(RELATED_IMAGE_OPENSCAP_TAG)%$(RELATED_IMAGE_OPENSCAP_PATH)%' deploy/operator.yaml
	@oc apply -n $(NAMESPACE) -f deploy/
	@oc apply -f deploy/olm-catalog/compliance-operator/manifests/monitoring_clusterrolebinding.yaml
	@oc apply -f deploy/olm-catalog/compliance-operator/manifests/monitoring_clusterrole.yaml
	@$(SED) 's%$(RELATED_IMAGE_OPERATOR_PATH)%$(IMAGE_REPO)/$(APP_NAME):latest%' deploy/operator.yaml
	@$(SED) 's%$(RELATED_IMAGE_OPENSCAP_PATH)%$(IMAGE_REPO)/$(RELATED_IMAGE_OPENSCAP_NAME):$(RELATED_IMAGE_OPENSCAP_TAG)%' deploy/operator.yaml
	@oc set triggers -n $(NAMESPACE) deployment/compliance-operator --from-image openshift/compliance-operator:latest -c compliance-operator

.PHONY: deploy-crds
deploy-crds:
	@for crd in $(shell ls -1 deploy/crds/*crd.yaml) ; do \
		oc apply -f $$crd ; \
	done

.PHONY: tear-down
tear-down: tear-down-crds tear-down-operator ## Tears down all objects required for the operator except the namespace


.PHONY: tear-down-crds
tear-down-crds:
	@for crd in $(shell ls -1 deploy/crds/*crd.yaml) ; do \
		oc delete --ignore-not-found -f $$crd ; \
	done

.PHONY: tear-down-operator
tear-down-operator:
	@for manifest in $(shell ls -1 deploy/*.yaml | grep -v ns.yaml) ; do \
		oc delete --ignore-not-found -f $$manifest ; \
	done


.PHONY: openshift-user
openshift-user:
ifeq ($(shell oc whoami 2> /dev/null),kube:admin)
	$(eval OPENSHIFT_USER = kubeadmin)
else
	$(eval OPENSHIFT_USER = $(shell oc whoami))
endif

.PHONY: push
push: image push-bundle
	# compliance-operator manager
	$(RUNTIME) push $(RELATED_IMAGE_OPERATOR_PATH):$(TAG)

.PHONY: must-gather-push
must-gather-push: must-gather-image
	$(RUNTIME) push $(MUST_GATHER_IMAGE_PATH):$(MUST_GATHER_IMAGE_TAG)

.PHONY: push-bundle
push-bundle: bundle-image
	# bundle image
	$(RUNTIME) push $(BUNDLE_IMAGE_PATH):$(BUNDLE_IMAGE_TAG)

.PHONY: push-test-bundle
push-test-bundle: test-bundle-image
	# test bundle image
	$(RUNTIME) push $(BUNDLE_IMAGE_PATH):$(TEST_BUNDLE_IMAGE_TAG)

.PHONY: push-index
push-index: index-image
	# index image
	$(RUNTIME) push $(INDEX_IMAGE_PATH):latest

.PHONY: push-openscap-image
push-openscap-image: openscap-image
	# openscap image
	$(RUNTIME) push $(RELATED_IMAGE_OPENSCAP_PATH):$(RELATED_IMAGE_OPENSCAP_TAG)

.PHONY: check-operator-version
check-operator-version:
ifndef OPERATOR_VERSION
	$(error OPERATOR_VERSION must be defined)
endif

.PHONY: bundle
bundle: check-operator-version operator-sdk ## Generate the bundle and packaging for the specific version (NOTE: Gotta specify the version with the OPERATOR_VERSION environment variable)
	$(GOPATH)/bin/operator-sdk generate bundle -q --overwrite --version "$(OPERATOR_VERSION)"
	sed -i '/replaces:/d' deploy/olm-catalog/compliance-operator/manifests/compliance-operator.clusterserviceversion.yaml
	sed -i "s/\(olm.skipRange: '>=.*\)<.*'/\1<$(OPERATOR_VERSION)'/" deploy/olm-catalog/compliance-operator/manifests/compliance-operator.clusterserviceversion.yaml
	sed -i "s/^appVersion:.*/appVersion: \"${OPERATOR_VERSION}\"/g" deploy/compliance-operator-chart/Chart.yaml
	$(GOPATH)/bin/operator-sdk bundle validate ./deploy/olm-catalog/compliance-operator/

.PHONY: package-version-to-tag
package-version-to-tag: check-operator-version
	@echo "Overriding default tag '$(TAG)' with release tag '$(OPERATOR_VERSION)'"
	$(eval TAG = $(OPERATOR_VERSION))

.PHONY: release-tag-image
release-tag-image: package-version-to-tag
	@echo "Temporarily overriding image tags in deploy/operator.yaml"
	@$(SED) 's%$(IMAGE_REPO)/$(APP_NAME):latest%$(RELATED_IMAGE_OPERATOR_PATH):$(TAG)%' deploy/operator.yaml

.PHONY: undo-deploy-tag-image
undo-deploy-tag-image: package-version-to-tag
	@echo "Restoring image tags in deploy/operator.yaml"
	@$(SED) 's%$(RELATED_IMAGE_OPERATOR_PATH):$(TAG)%$(IMAGE_REPO)/$(APP_NAME):latest%' deploy/operator.yaml

.PHONY: git-release
git-release: package-version-to-tag changelog
	git checkout -b "release-v$(TAG)"
	sed -i "s/\(.*Version = \"\).*/\1$(TAG)\"/" version/version.go
	git add "version/version.go"
	git add "deploy/olm-catalog/compliance-operator/"
	git add "deploy/compliance-operator-chart/Chart.yaml"
	git add "CHANGELOG.md"

.PHONY: fetch-git-tags
fetch-git-tags:
	# Make sure we are caught up with tags
	git fetch -t

.PHONY: prepare-release
prepare-release: release-tag-image bundle git-release

.PHONY: push-release
push-release: package-version-to-tag ## Do an official release (Requires permissions)
	git commit -m "Release v$(TAG)"
	git tag "v$(TAG)"
	git push origin "v$(TAG)"
	git push origin "release-v$(TAG)"

.PHONY: release-images
release-images: package-version-to-tag push push-index undo-deploy-tag-image
	# This will ensure that we also push to the latest tag
	$(MAKE) push TAG=latest

.PHONY: changelog
changelog:
	@utils/update_changelog.sh "$(TAG)"
