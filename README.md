# compliance-operator

The compliance-operator is a OpenShift Operator that allows an administrator
to run compliance scans and provide remediations for the issues found. The
operator leverages OpenSCAP under the hood to perform the scans.

By default, the operator runs in the `openshift-compliance` namespace, so
make sure all namespaced resources like the deployment or the custom resources
the operator consumes are created there. However, it is possible for the
operator to be deployed in other namespaces as well.

The primary interface towards the Compliance Operator is the
`ComplianceSuite` object, representing a set of scans. The `ComplianceSuite`
can be defined either manually or with the help of `ScanSetting` and
`ScanSettingBinding` objects. Note that while it is possible to use the
lower-level `ComplianceScan` directly as well, it is not recommended.

## Deploying the operator
Before you can actually use the operator, you need to make sure it is
deployed in the cluster. Depending on your needs, you might want to
deploy the upstream released packages or directly from the source.

First, become kubeadmin, either with `oc login` or by exporting `KUBECONFIG`.

### Deploying upstream packages
Deploying from package would deploy the latest released upstream version.

First, create the `CatalogSource` and optionally verify it's been created
successfuly:
```
$ oc create -f deploy/olm-catalog/catalog-source.yaml
$ oc get catalogsource -nopenshift-marketplace
```

Next, create the target namespace and finally either install the operator
from the Web Console or from the CLI following these steps:
```
$ oc create -f deploy/ns.yaml
$ oc create -f deploy/olm-catalog/operator-group.yaml
$ oc create -f deploy/olm-catalog/subscription.yaml
```
The Subscription file can be edited to optionally deploy a custom version,
see the `startingCSV` attribute in the `deploy/olm-catalog/subscription.yaml`
file.

Verify that the expected objects have been created:
```
$ oc get sub -nopenshift-compliance
$ oc get ip -nopenshift-compliance
$ oc get csv -nopenshift-compliance
```

At this point, the operator should be up and running:
```
$ oc get deploy -nopenshift-compliance
$ oc get pods -nopenshift-compliance
```

### Deploying from source
```
$ (clone repo)
$ oc create -f deploy/ns.yaml
$ oc project openshift-compliance
$ for f in $(ls -1 deploy/crds/*crd.yaml); do oc apply -f $f -n openshift-compliance; done
$ oc apply -n openshift-compliance -f deploy/
```

### Running the operator locally
If you followed the steps above, the file called `deploy/operator.yaml`
also creates a deployment that runs the operator. If you want to run
the operator from the command line instead, delete the deployment and then
run:

```
make run
```
This is mostly useful for local development.

### Note on namespace removal
Many custom resources deployed with the compliance operators use finalizers
to handle dependencies between objects. If the whole operator namespace gets
deleted (e.g. with `oc delete ns openshift-compliance`), the order of deleting
objects in the namespace is not guaranteed. What can happen is that the
operator itself is removed before the finalizers are processed which would
manifest as the namespace being stuck in the `Terminating` state.

It is recommended to remove all CRs and CRDs prior to removing the namespace
to avoid this issue. The `Makefile` provides a `tear-down` target that does
exactly that.

If the namespace is stuck, you can work around by the issue by hand-editing
or patching any CRs and removing the `finalizers` attributes manually.


## Using the operator

Before starting to use the operator, it's worth checking the descriptions of the
different custom resources it introduces. These definitions are in the
[following document](doc/crds.md)

As part of this guide, it's assumed that you have installed the compliance operator
in the `openshift-compliance` namespace. So you can use:

```
# Set this to the namespace you're deploying the operator at
export NAMESPACE=openshift-compliance
```

There are several profiles that come out-of-the-box as part of the operator
installation.

To view them, use the following command:

```
$ oc get -n $NAMESPACE profiles.compliance
NAME              AGE
ocp4-cis          2m50s
ocp4-cis-node     2m50s
ocp4-e8           2m50s
ocp4-moderate     2m50s
rhcos4-e8         2m46s
rhcos4-moderate   2m46s
```

### Platform and Node scan types
These profiles define different compliance benchmarks and as well as
the scans fall into two basic categories - platform and node. The
platform scans are targetting the cluster itself, in the listing above
they're the `ocp4-*` scans, while the purpose of the node scans is to
scan the actual cluster nodes. All the `rhcos4-*` profiles above can be
used to create node scans.

Before taking one into use, we'll need to configure how the scans
will run. We can do this with the `ScanSetttings` custom resource. The
compliance-operator already ships with a default `ScanSettings` object
that you can take into use immediately:

```
$ oc get -n $NAMESPACE scansettings default -o yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: default
  namespace: openshift-compliance
rawResultStorage:
  rotation: 3
  size: 1Gi
roles:
- worker
- master
scanTolerations:
- effect: NoSchedule
  key: node-role.kubernetes.io/master
  operator: Exists
schedule: '0 1 * * *'
```

So, to assert the intent of complying with the `rhcos4-moderate` profile, we can use
the `ScanSettingBinding` custom resource. the example that already exists in this repo
will do just this.

```
$ cat deploy/crds/compliance.openshift.io_v1alpha1_scansettingbinding_cr.yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: nist-moderate
profiles:
  - name: ocp4-moderate
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
settingsRef:
  name: default
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
```

To take it into use, do the following:

```
$ oc create -n $NAMESPACE -f deploy/crds/compliance.openshift.io_v1alpha1_scansettingbinding_cr.yaml
scansettingbinding.compliance.openshift.io/nist-moderate created
```

At this point the operator reconciles a `ComplianceSuite` custom resource,
we can use this to track the progress of our scan.

```
$ oc get -n $NAMESPACE compliancesuites -w
NAME            PHASE     RESULT
nist-moderate   RUNNING   NOT-AVAILABLE
```

You can also make use of conditions to wait for a suite to produce results:
```
$ oc wait --for=condition=ready compliancesuite cis-compliancesuite
```

This subsequently creates the `ComplianceScan` objects for the suite.
The `ComplianceScan` then creates scan pods that run on each node in
the cluster. The scan pods execute `openscap-chroot` on every node and
eventually report the results. The scan takes several minutes to complete.

If you're interested in seeing the individual pods, you can do so with:
```
$ oc get -n $NAMESPACE pods -w
```

When the scan is done, the operator changes the state of the ComplianceSuite
object to "Done" and all the pods are transition to the "Completed"
state. You can then check the `ComplianceRemediations` that were found with:
```
$ oc get -n $NAMESPACE complianceremediations
NAME                                                             STATE
workers-scan-auditd-name-format                                  NotApplied
workers-scan-coredump-disable-backtraces                         NotApplied
workers-scan-coredump-disable-storage                            NotApplied
workers-scan-disable-ctrlaltdel-burstaction                      NotApplied
workers-scan-disable-users-coredumps                             NotApplied
workers-scan-grub2-audit-argument                                NotApplied
workers-scan-grub2-audit-backlog-limit-argument                  NotApplied
workers-scan-grub2-page-poison-argument                          NotApplied
```

To apply a remediation, edit that object and set its `Apply` attribute
to `true`:
```
$ oc edit -n $NAMESPACE complianceremediation/workers-scan-no-direct-root-logins
```

The operator then creates a `MachineConfig` object per remediation. This
`MachineConfig` object is rendered to a `MachinePool` and the
`MachineConfigDeamon` running on nodes in that pool pushes the configuration
to the nodes and reboots the nodes.

You can watch the node status with:
```
$ oc get nodes -w
```

Once the nodes reboot, you might want to run another Suite to ensure that
the remediation that you applied previously was no longer found.

## Extracting raw results

The scans provide two kinds of raw results: the full report in the ARF format
and just the list of scan results in the XCCDF format. The ARF reports are,
due to their large size, copied into persistent volumes:
```
$ oc get pv
NAME                                       CAPACITY  CLAIM
pvc-5d49c852-03a6-4bcd-838b-c7225307c4bb   1Gi       openshift-compliance/workers-scan
pvc-ef68c834-bb6e-4644-926a-8b7a4a180999   1Gi       openshift-compliance/masters-scan
$ oc get pvc
NAME                     STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
ocp4-moderate            Bound    pvc-01b7bd30-0d19-4fbc-8989-bad61d9384d9   1Gi        RWO            gp2            37m
rhcos4-with-usb-master   Bound    pvc-f3f35712-6c3f-42f0-a89a-af9e6f54a0d4   1Gi        RWO            gp2            37m
rhcos4-with-usb-worker   Bound    pvc-7837e9ba-db13-40c4-8eee-a2d1beb0ada7   1Gi        RWO            gp2            37m
```

An example of extracting ARF results from a scan called `workers-scan` follows:

Once the scan had finished, you'll note that there is a `PersistentVolumeClaim` named
after the scan:
```
oc get pvc/workers-scan
NAME            STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
workers-scan    Bound    pvc-01b7bd30-0d19-4fbc-8989-bad61d9384d9   1Gi        RWO            gp2            38m
```
You'll want to start a pod that mounts the PV, for example:
```yaml
apiVersion: "v1"
kind: Pod
metadata:
  name: pv-extract
spec:
  containers:
    - name: pv-extract-pod
      image: registry.access.redhat.com/ubi8/ubi
      command: ["sleep", "3000"]
      volumeMounts:
        - mountPath: "/workers-scan-results"
          name: workers-scan-vol
  volumes:
    - name: workers-scan-vol
      persistentVolumeClaim:
        claimName: workers-scan
```

You can inspect the files by listing the `/workers-scan-results` directory and copy the
files locally:
```
$ oc exec pods/pv-extract -- ls /workers-scan-results/0
lost+found
workers-scan-ip-10-0-129-252.ec2.internal-pod.xml.bzip2
workers-scan-ip-10-0-149-70.ec2.internal-pod.xml.bzip2
workers-scan-ip-10-0-172-30.ec2.internal-pod.xml.bzip2
$ oc cp pv-extract:/workers-scan-results .
```
The files are bzipped. To get the raw ARF file:
```
$ bunzip2 -c workers-scan-ip-10-0-129-252.ec2.internal-pod.xml.bzip2 > workers-scan-ip-10-0-129-252.ec2.internal-pod.xml
```

The XCCDF results are much smaller and can be stored in a configmap, from
which you can extract the results. For easier filtering, the configmaps
are labeled with the scan name:
```
$ oc get cm -l=compliance.openshift.io/scan-name=masters-scan
NAME                                            DATA   AGE
masters-scan-ip-10-0-129-248.ec2.internal-pod   1      25m
masters-scan-ip-10-0-144-54.ec2.internal-pod    1      24m
masters-scan-ip-10-0-174-253.ec2.internal-pod   1      25m
```

To extract the results, use:
```
$ oc extract cm/masters-scan-ip-10-0-174-253.ec2.internal-pod
```

Note that if the results are too big for the ConfigMap, they'll be bzipped and
base64 encoded.

OS support
==========

Node scans
----------

Note that the current testing has been done in RHCOS. In the absence of
RHEL/CentOS support, one can simply run OpenSCAP directly on the nodes.

Platform scans
--------------

Current testing has been done on OpenShift (OCP). The project is open to
getting other platforms tested, so volunteers are needed for this.

The current supported versions of OpenShift are 4.6 and up.

Additional documentation
========================

See the [self-paced workshop](doc/tutorials/README.md) for a hands-on tutorial,
including advanced topics such as content building.
