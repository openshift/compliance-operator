# Step one: build fetcher
FROM registry.access.redhat.com/ubi8/go-toolset as builder

WORKDIR /go/src/github.com/openshift/compliance-operator

ENV GOFLAGS=-mod=vendor
COPY . .
RUN make api-resource-collector

# Step two: containerize fetcher
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

USER root

# build fetcher
COPY --from=builder /go/src/github.com/openshift/compliance-operator/build/_output/bin/api-resource-collector /usr/local/bin/api-resource-collector

ENTRYPOINT ["/usr/local/bin/api-resource-collector"]
