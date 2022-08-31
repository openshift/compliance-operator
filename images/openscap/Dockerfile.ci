FROM registry.access.redhat.com/ubi8/ubi-minimal

LABEL \
    name="openscap-ocp" \
    run="podman run --privileged -v /:/host  -eHOSTROOT=/host -ePROFILE=xccdf_org.ssgproject.content_profile_coreos-fedramp -eCONTENT=ssg-rhcos4-ds.xml -eREPORT_DIR=/reports -eRULE=xccdf_org.ssgproject.content_rule_selinux_state" \
    io.k8s.display-name="OpenSCAP container for OCP4 node scans" \
    io.k8s.description="OpenSCAP security scanner for scanning hosts through a host mount" \
    io.openshift.tags="compliance openscap scan" \
    io.openshift.wants="scap-content"

RUN true \
    && microdnf install -y openscap-scanner \
    && microdnf clean all \
    && true

RUN mkdir /app
RUN rpm -q openscap-scanner > /app/scap_version
