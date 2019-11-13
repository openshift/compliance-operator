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

# These will be provided to the target
#VERSION := 1.0.0
#BUILD := `git rev-parse HEAD`

# Use linker flags to provide version/build settings to the target
#LDFLAGS=-ldflags "-X=main.Version=$(VERSION) -X=main.Build=$(BUILD)"

# go source files, ignore vendor directory
SRC = $(shell find . -type f -name '*.go' -not -path "./vendor/*" -not -path "./_output/*")

#.PHONY: all build clean install uninstall fmt simplify check run
.PHONY: all operator-sdk build clean fmt simplify gendeepcopy test-unit run

all: build #check install

build: fmt
	operator-sdk build $(IMAGE_PATH)

run:
	WATCH_NAMESPACE=$(NAMESPACE) \
	KUBERNETES_CONFIG=$(KUBECONFIG) \
	operator-sdk up local --namespace $(NAMESPACE)

clean:
	@rm -rf $(TARGET_DIR)

fmt:
	@gofmt -l -w cmd && \
	gofmt -l -w pkg

simplify:
	@gofmt -s -l -w $(SRC)

gendeepcopy: operator-sdk
	@GO111MODULE=on operator-sdk generate k8s

test-unit: fmt
	@$(GO) test $(TEST_OPTIONS) $(PKGS)

push: build
	$(RUNTIME) tag $(IMAGE_PATH) $(IMAGE_PATH):$(TAG)
	$(RUNTIME) push $(IMAGE_PATH):$(TAG)
