FROM registry.access.redhat.com/ubi8/ubi-minimal

LABEL \
    name="openscap-ocp" \
    run="podman run --privileged -v /:/host  -eHOSTROOT=/host -ePROFILE=xccdf_org.ssgproject.content_profile_coreos-fedramp -eCONTENT=ssg-rhcos4-ds.xml -eREPORT_DIR=/reports -eRULE=xccdf_org.ssgproject.content_rule_selinux_state" \
    io.k8s.display-name="OpenSCAP container for OCP4 node scans" \
    io.k8s.description="OpenSCAP security scanner for scanning hosts through a host mount" \
    io.openshift.tags="compliance openscap scan" \
    io.openshift.wants="scap-content"

RUN echo $'[openscap-1-3-5-copr]\n\
name=Copr repo for openscap-1-3-5 owned by jhrozek\n\
baseurl=https://download.copr.fedorainfracloud.org/results/jhrozek/openscap-1-3-5/epel-8-$basearch/\n\
type=rpm-md\n\
skip_if_unavailable=True\n\
gpgcheck=1\n\
gpgkey=https://download.copr.fedorainfracloud.org/results/jhrozek/openscap-1-3-5/pubkey.gpg\n\
repo_gpgcheck=0\n\
enabled=1\n\
enabled_metadata=1\n '\
>> /etc/yum.repos.d/openscap.repo

RUN true \
    && microdnf install -y openscap-scanner \
    && microdnf clean all \
    && true

RUN mkdir /app
RUN rpm -q openscap-scanner > /app/scap_version
