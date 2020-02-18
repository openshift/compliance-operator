# Step one: build aggregator
FROM registry.access.redhat.com/ubi8/go-toolset as builder

WORKDIR /go/src/github.com/openshift/compliance-operator

ENV GOFLAGS=-mod=vendor
COPY . .
RUN make remediation-aggregator

# Step two: containerize aggregator
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

USER root

# build aggregator
COPY --from=builder /go/src/github.com/openshift/compliance-operator/build/_output/bin/aggregator /usr/local/bin/aggregator

ENTRYPOINT ["/usr/local/bin/aggregator"]
