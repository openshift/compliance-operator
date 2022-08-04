# Usage Guide

Before starting to use the operator, it's worth checking the descriptions of
the different custom resources it introduces. These definitions are in the
[following
document](https://github.com/openshift/compliance-operator/blob/master/doc/crds.md).
The primary interface for the compliance-operator is the `ComplianceSuite`
object, representing a set of scans. The `ComplianceSuite` can be defined
either manually or with the help of `ScanSetting` and `ScanSettingBinding`
objects. Note that while it is possible to use the lower-level `ComplianceScan`
directly as well, it is not recommended.

As part of this guide, it's assumed that you have installed the compliance operator
in the `openshift-compliance` namespace. You can find more information about
installation methods and directions in the [Installation
Guide](https://github.com/openshift/compliance-operator/blob/master/doc/install.md).

After you've installed the operator, set the `NAMESPACE` environment to the
namespace you installed the operator. By default, the operator is installed in
the `openshift-compliance` namespace.

```
export NAMESPACE=openshift-compliance
```

## Listing profiles

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

## Scan types

These profiles define different compliance benchmarks and as well as
the scans fall into two basic categories - platform and node. The
platform scans are targeting the cluster itself, in the listing above
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
the `ScanSettingBinding` custom resource. The example that already exists in this repo
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

## Operating system support

### Node scans

Note that the current testing has been done in RHCOS. In the absence of
RHEL/CentOS support, one can simply run OpenSCAP directly on the nodes.

### Platform scans

Current testing has been done on OpenShift (OCP). The project is open to
getting other platforms tested, so volunteers are needed for this.

The current supported versions of OpenShift are 4.6 and up.

## Additional documentation

See the [self-paced workshop](tutorials/README.md) for a hands-on tutorial,
including advanced topics such as content building.

## Must-gather support

An `oc adm must-gather` image for collecting operator information for debugging
or support is available at `quay.io/compliance-operator/must-gather:latest`:

```
$ oc adm must-gather --image=quay.io/compliance-operator/must-gather:latest
```

## Metrics

The compliance-operator exposes the following metrics to Prometheus when cluster-monitoring is available.

    # HELP compliance_operator_compliance_remediation_status_total A counter
    # for the total number of updates to the status of a ComplianceRemediation
    # TYPE compliance_operator_compliance_remediation_status_total counter
    compliance_operator_compliance_remediation_status_total{name="remediation-name",state="NotApplied"} 1

    # HELP compliance_operator_compliance_scan_status_total A counter for the
    # total number of updates to the status of a ComplianceScan
    # TYPE compliance_operator_compliance_scan_status_total counter
    compliance_operator_compliance_scan_status_total{name="scan-name",phase="AGGREGATING",result="NOT-AVAILABLE"} 1

    # HELP compliance_operator_compliance_scan_error_total A counter for the
    # total number of encounters of error
    # TYPE compliance_operator_compliance_scan_error_total counter
    compliance_operator_compliance_scan_error_total{name="scan-name",error="some_error"} 1

    # HELP compliance_operator_compliance_state A gauge for the compliance
    # state of a ComplianceSuite. Set to 0 when COMPLIANT, 1 when NON-COMPLIANT,
    # 2 when INCONSISTENT, and 3 when ERROR
    # TYPE compliance_operator_compliance_state gauge
    compliance_operator_compliance_state{name="some-compliance-suite"} 1

After logging into the console, navigating to Monitoring -> Metrics, the
compliance_operator* metrics can be queried using the metrics dashboard. The
`{__name__=~"compliance.*"}` query can be used to view the full set of metrics.

Testing for the metrics from the cli can also be done directly with a pod that
curls the metrics service. This is useful for troubleshooting.

```
oc run --rm -i --restart=Never --image=registry.fedoraproject.org/fedora-minimal:latest -n openshift-compliance metrics-test -- bash -c 'curl -ks -H "Authorization: Bea
rer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" https://metrics.openshift-compliance.svc:8585/metrics-co' | grep compliance
```

## To use PriorityClass for scans

When heavily using Pod Priority and Preemption[1] for automated scaling and
the default `PriorityClass` is too low to guarantee pods to run then scans
are not executed and reports are missing. Since the Compliance Operator
is important for ensuring compliance, we should give administrators the
ability to associate a `PriorityClass` with the operator. This will ensure
the Compliance Operator is prioritized and minimizes the chance that the
cluster will fall out of compliance because the Compliance Operator wasn’t
running.

An admin can set PriorityClass[1] in `ScanSetting`, below is an example of a
`ScanSetting` with a PriorityClass:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
strictNodeScan: true
metadata:
  name: default
  namespace: openshift-compliance
priorityClass: compliance-high-priority
kind: ScanSetting
showNotApplicable: false
rawResultStorage:
  nodeSelector:
    node-role.kubernetes.io/master: ''
  pvAccessModes:
    - ReadWriteOnce
  rotation: 3
  size: 1Gi
  tolerations:
    - effect: NoSchedule
      key: node-role.kubernetes.io/master
      operator: Exists
    - effect: NoExecute
      key: node.kubernetes.io/not-ready
      operator: Exists
      tolerationSeconds: 300
    - effect: NoExecute
      key: node.kubernetes.io/unreachable
      operator: Exists
      tolerationSeconds: 300
    - effect: NoSchedule
      key: node.kubernetes.io/memory-pressure
      operator: Exists
schedule: 0 1 * * *
roles:
  - master
  - worker
scanTolerations:
  - operator: Exists
```

If the `PriorityClass` referenced in the ScanSetting can't be found,
the operator will leave `PriorityClass` empty, issue a warning, and
continue scheduling scans without a `PriorityClass`.

[1]: https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#priorityclass
