# Step one: build resultscollector
FROM registry.access.redhat.com/ubi8/go-toolset as builder

WORKDIR /go/src/github.com/openshift/compliance-operator

ENV GOFLAGS=-mod=vendor
COPY . .
RUN make build

# Step two: containerize resultscollector
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

USER root

# build resultscollector
COPY --from=builder /go/src/github.com/openshift/compliance-operator/build/_output/bin/resultscollector /usr/local/bin/resultscollector

ENTRYPOINT ["/usr/local/bin/resultscollector"]
