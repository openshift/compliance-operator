FROM registry.access.redhat.com/ubi8/ubi-minimal

ARG xml_path
COPY $xml_path/* .
