CURPATH=$(PWD)
TARGET_DIR=$(CURPATH)/build/_output
KUBECONFIG?=$(HOME)/.kube/config

GO=GO111MODULE=on go
GOBUILD=$(GO) build
BUILD_GOPATH=$(TARGET_DIR):$(CURPATH)/cmd
RUNTIME?=docker

export APP_NAME=compliance-operator
IMAGE_REPO?=quay.io/jhrozek
IMAGE_PATH?=$(IMAGE_REPO)/$(APP_NAME)
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
.PHONY: all operator-sdk image build clean clean-cache clean-modcache clean-output fmt simplify verify vet mod-verify gosec gendeepcopy test-unit run e2e

all: build #check install

image: fmt operator-sdk
	$(GOPATH)/bin/operator-sdk build $(IMAGE_PATH) --image-builder $(RUNTIME)

build: fmt
	$(GO) build -o $(TARGET) github.com/openshift/compliance-operator/cmd/manager

operator-sdk:
ifeq ("$(wildcard $(GOPATH)/bin/operator-sdk)","")
	wget $(OPERATOR_SDK_URL) -O $(GOPATH)/bin/operator-sdk || (echo "wget returned $$? trying to fetch operator-sdk. please install operator-sdk and try again"; exit 1)
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

e2e: operator-sdk
	@echo "Creating '$(NAMESPACE)' namespace/project"
	@oc create -f deploy/ns.yaml || true
	@echo "Running e2e tests"
	@$(GOPATH)/bin/operator-sdk test local ./tests/e2e --namespace "$(NAMESPACE)" --go-test-flags "-v"

push: image
	$(RUNTIME) tag $(IMAGE_PATH) $(IMAGE_PATH):$(TAG)
	$(RUNTIME) push $(IMAGE_PATH):$(TAG)
