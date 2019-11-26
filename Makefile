CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/build/_output
KUBECONFIG?=$(HOME)/.kube/config
# Skip pushing the container to your cluster
E2E_SKIP_CONTAINER_PUSH?=false

GO=GO111MODULE=on go
GOBUILD=$(GO) build
BUILD_GOPATH=$(TARGET_DIR):$(CURPATH)/cmd
RUNTIME?=docker

export APP_NAME=compliance-operator
IMAGE_REPO?=quay.io/jhrozek

# Image path to use. Set this if you want to use a specific path for building
# or your e2e tests. This is overwritten if we bulid the image and push it to
# the cluster or if we're on CI.
IMAGE_PATH?=$(IMAGE_REPO)/$(APP_NAME)

# Image tag to use. Set this if you want to use a specific tag for building
# or your e2e tests.
TAG?=latest

TARGET=$(TARGET_DIR)/bin/$(APP_NAME)
MAIN_PKG=cmd/manager/main.go
export NAMESPACE?=openshift-compliance

PKGS=$(shell go list ./... | grep -v -E '/vendor/|/test|/examples')

TEST_OPTIONS?=

OC?=oc

SDK_VERSION?=v0.12.0
OPERATOR_SDK_URL=https://github.com/operator-framework/operator-sdk/releases/download/$(SDK_VERSION)/operator-sdk-$(SDK_VERSION)-x86_64-linux-gnu

# These will be provided to the target
#VERSION := 1.0.0
#BUILD := `git rev-parse HEAD`

# Use linker flags to provide version/build settings to the target
#LDFLAGS=-ldflags "-X=main.Version=$(VERSION) -X=main.Build=$(BUILD)"

# go source files, ignore vendor directory
SRC = $(shell find . -type f -name '*.go' -not -path "./vendor/*" -not -path "./_output/*")

#.PHONY: all build clean install uninstall fmt simplify check run
.PHONY: all operator-sdk image build clean clean-cache clean-modcache clean-output fmt simplify verify vet mod-verify gosec gendeepcopy test-unit run e2e check-if-ci

all: build #check install

image: fmt operator-sdk
	$(GOPATH)/bin/operator-sdk build $(IMAGE_PATH) --image-builder $(RUNTIME)

build: fmt
	$(GO) build -o $(TARGET) github.com/openshift/compliance-operator/cmd/manager

operator-sdk:
ifeq ("$(wildcard $(GOPATH)/bin/operator-sdk)","")
	wget -nv $(OPERATOR_SDK_URL) -O $(GOPATH)/bin/operator-sdk || (echo "wget returned $$? trying to fetch operator-sdk. please install operator-sdk and try again"; exit 1)
	chmod +x $(GOPATH)/bin/operator-sdk
endif

run: operator-sdk
	WATCH_NAMESPACE=$(NAMESPACE) \
	KUBERNETES_CONFIG=$(KUBECONFIG) \
	OPERATOR_NAME=compliance-operator \
	$(GOPATH)/bin/operator-sdk up local --namespace $(NAMESPACE)

clean: clean-modcache clean-cache clean-output

clean-output:
	rm -rf $(TARGET_DIR)

clean-cache:
	$(GO) clean -cache -testcache $(PKGS)

clean-modcache:
	$(GO) clean -modcache $(PKGS)

fmt:
	@$(GO) fmt $(PKGS)

simplify:
	@gofmt -s -l -w $(SRC)

verify: vet mod-verify gosec

vet:
	@$(GO) vet $(PKGS)

mod-verify:
	@$(GO) mod verify

gosec:
	@$(GO) run github.com/securego/gosec/cmd/gosec -severity medium -confidence medium -quiet ./...

gendeepcopy: operator-sdk
	@GO111MODULE=on $(GOPATH)/bin/operator-sdk generate k8s

test-unit: fmt
	@$(GO) test $(TEST_OPTIONS) $(PKGS)

# This runs the end-to-end tests. If not running this on CI, it'll try to
# push the operator image to the cluster's registry. This behavior can be
# avoided with the E2E_SKIP_CONTAINER_PUSH environment variable.
ifeq ($(E2E_SKIP_CONTAINER_PUSH), false)
e2e: operator-sdk check-if-ci image-to-cluster
else
e2e: operator-sdk check-if-ci
endif
	@echo "Creating '$(NAMESPACE)' namespace/project"
	@oc create -f deploy/ns.yaml || true
	@echo "Running e2e tests"
	$(GOPATH)/bin/operator-sdk test local ./tests/e2e --image "$(IMAGE_PATH)" --namespace "$(NAMESPACE)" --go-test-flags "-v"

# This checks if we're in a CI environment by checking the IMAGE_FORMAT
# environmnet variable. if we are, lets ues the image from CI and use this
# operator as the component.
#
# The IMAGE_FORMAT variable comes from CI. It is of the format:
#     <image path in CI registry>:${component}
# Here define the `component` variable, so, when we overwrite the
# IMAGE_PATH variable, it'll expand to the component we need.
check-if-ci:
ifdef IMAGE_FORMAT
	$(eval component = $(APP_NAME))
	$(eval IMAGE_PATH = $(IMAGE_FORMAT))
endif

# If IMAGE_FORMAT is not defined, it means that we're not running on CI, so we
# probably want to push the compliance-operator image to the cluster we're
# developing on. This target exposes temporarily the image registry, pushes the
# image, and remove the route in the end.
.PHONY: image-to-cluster
image-to-cluster: openshift-user image
ifndef IMAGE_FORMAT
	@echo "Temporarily exposing the default route to the image registry"
	@oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":true}}' --type=merge
	@echo "Pushing image $(IMAGE_PATH):$(TAG) to the image registry"
	IMAGE_REGISTRY_HOST=$$(oc get route default-route -n openshift-image-registry --template='{{ .spec.host }}'); \
		$(RUNTIME) login --tls-verify=false -u $(OPENSHIFT_USER) -p $(shell oc whoami -t) $${IMAGE_REGISTRY_HOST}; \
		$(RUNTIME) push --tls-verify=false $(IMAGE_PATH):$(TAG) $${IMAGE_REGISTRY_HOST}/$(NAMESPACE)/$(APP_NAME):$(TAG)
	@echo "Removing the route from the image registry"
	@oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":false}}' --type=merge
	$(eval IMAGE_PATH = image-registry.openshift-image-registry.svc:5000/$(NAMESPACE)/$(APP_NAME):$(TAG))
endif

.PHONY: openshift-user
openshift-user:
ifeq ($(shell oc whoami),kube:admin)
	$(eval OPENSHIFT_USER = kubeadmin)
else
	$(eval OPENSHIFT_USER = $(oc whoami))
endif

push: image
	$(RUNTIME) tag $(IMAGE_PATH) $(IMAGE_PATH):$(TAG)
	$(RUNTIME) push $(IMAGE_PATH):$(TAG)
