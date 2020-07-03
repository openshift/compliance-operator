# compliance-operator

The compliance-operator is a OpenShift Operator that allows an administrator
to run compliance scans and provide remediations for the issues found. The
operator leverages OpenSCAP under the hood to perform the scans.

By default, the operator runs in the `openshift-compliance` namespace, so
make sure all namespaced resources like the deployment or the custom resources
the operator consumes are created there. However, it is possible for the
operator to be deployed in other namespaces as well.

## The Objects

### The `ComplianceSuite` object

The API resource that you'll be interacting with is called `ComplianceSuite`.
It is a collection of `ComplianceScan` objects, each of which describes
a scan. In addition, the `ComplianceSuite` also contains an overview of the
remediations found and the statuses of the scans. Typically, you'll want
to map a scan to a `MachinePool`, mostly because the remediations for any
issues that would be found contain `MachineConfig` objects that must be
applied to a pool.

A `ComplianceSuite` will look similar to this:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceSuite
metadata:
  name: fedramp-moderate
spec:
  autoApplyRemediations: false
  schedule: "0 1 * * *"
  scans:
    - name: workers-scan
      scanType: Node
      profile: xccdf_org.ssgproject.content_profile_moderate
      content: ssg-rhcos4-ds.xml
      contentImage: quay.io/complianceascode/ocp4:latest
      rule: "xccdf_org.ssgproject.content_rule_no_netrc_files"
      nodeSelector:
        node-role.kubernetes.io/worker: ""
status:
  aggregatedPhase: DONE
  aggregatedResult: NON-COMPLIANT
  scanStatuses:
  - name: workers-scan
    phase: DONE
    result: NON-COMPLIANT
```
In the `spec`:
* **autoApplyRemediations**: Specifies if any remediations found from the
  scan(s) should be applied automatically.
* **schedule**: Defines how often should the scan(s) be run in cron format.
* **scans** contains a list of scan specifications to run in the cluster.

In the `status`:
* **aggregatedPhase**: indicates the overall phase where the scans are at. To
  get the results you normally have to wait for the overall phase to be `DONE`.
* **aggregatedResult**: Is the overall verdict of the suite.
* **scanStatuses**: Will contain the status for each of the scans that the
  suite is tracking.

The suite in the background will create as many `ComplianceScan` objects as you
specify in the `scans` field. The fields will be described in the section
referring to `ComplianceScan` objects.

Note that `ComplianceSuites` will generate events which you can fetch
programmatically. For instance, to get the events for the suite called
`example-compliancesuite` you could use the following command:

```
oc get events --field-selector involvedObject.kind=ComplianceSuite,involvedObject.name=example-compliancesuite
LAST SEEN   TYPE     REASON            OBJECT                                    MESSAGE
23m         Normal   ResultAvailable   compliancesuite/example-compliancesuite   ComplianceSuite's result is: NON-COMPLIANT
```

This will also show up in the output of the `oc describe` command.

### The `ComplianceScan` object

Similarly to `Pods` in Kubernetes, a `ComplianceScan` is the base object that
the compliance-operator introduces. Also similarly to `Pods`, you normally
don't want to create a `ComplianceScan` object directly, and would instead want
a `ComplianceSuite` to manage it.

Let's look at an example:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceScan
metadata:
  name: worker-scan
spec:
  scanType: Node
  profile: xccdf_org.ssgproject.content_profile_moderate
  content: ssg-ocp4-ds.xml
  contentImage: quay.io/complianceascode/ocp4:latest
  rule: "xccdf_org.ssgproject.content_rule_no_netrc_files"
  nodeSelector:
    node-role.kubernetes.io/worker: ""
status:
  phase: DONE
  result: NON-COMPLIANT
```

In the `spec`:

* **scanType**: Is the type of scan to execute. There are currently two types:
  - **Node**: Is meant to run on the host that runs the containers. This spawns
    a privileged pod that mounts the host's filesystem and executes security
    checks directly on it.
  - **Platform**: Is meant to run checks related to the platform itself (the
    Kubernetes distribution). This will run a non-privileged pod that will
    fetch resources needed in the checks and will subsequently run the scan.
* **profile**: Is the XCCDF identifier of the profile that you want to run. A
  profile is a set of rules that check for a specific compliance target. In the
  example `xccdf_org.ssgproject.content_profile_moderate` checks for rules
  required in the NIST SP 800-53 moderate profile.
* **contentImage**: The security checklist definition or datastream
  (the XCCDF/SCAP file) will need to come from a container image. This is
  where the image is specified.
* **rule**: Optionally, you can tell the scan to run a single rule. This rule
  has to be identified with the XCCDF ID, and has to belong to the specified
  profile. Note that you can skip this parameter, and if so, the scan will run
  all the rules available for the specified profile.
* **nodeSelector**: For `Node` scan types, you normally want to encompass a
  specific type of node, this is achievable by specifying the `nodeSelector`.
  If you're running on OpenShift and want to generate remediations, this label
  has to match the label for the `MachineConfigPool` that the `MachineConfig`
  remediation will be created for. Note that if this parameter is not
  specified or doesn't match a `MachineConfigPool`, a scan will still be run,
  but remediations won't be created.
* **rawResultStorage.size**: Specifies the size of storage that should be asked
  for in order for the scan to store the raw results. (Defaults to 1Gi)
* **rawResultStorage.rotation**: Specifies the amount of scans for which the raw
  results will be stored. Older results will get rotated, and it's the
  responsibility of administrators to store these results elsewhere before
  rotation happens. Note that a rotation policy of '0' disables rotation
  entirely. Defaults to 3.
* **scanTolerations**: Specifies tolerations that will be set in the scan Pods
  for scheduling. Defaults to allowing the scan to run on master nodes. For
  details on tolerations, see the
  [Kubernetes documentation on this](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/).

Regarding the `status`:

* **phase**: Indicates the phase the scan is at. You have to wait
  for a scan to be in the state `DONE` to get the results.
* **result**: Indicates the verdict of the scan. The scan can be `COMPLIANT`,
  `NON-COMPLIANT`, or report an `ERROR` if an unforeseen issue happened or
  there's an issue in the scan specification.

When a scan is created by a suite, the scan is owned by it. Deleting a
`ComplianceSuite` object will result in deleting all the scans that it created.

Once a scan has finished running it'll generate the results as Custom Resources
of the `ComplianceCheckResult` kind. However, the raw results in ARF format
will also be available. These will be stored in a Persistent Volume which has a
Persistent Volume Claim associated that has the same name as the scan.

Note that `ComplianceScans` will generate events which you can fetch
programmatically. For instance, to get the events for the suite called
`workers-scan` you could use the following command:

```
oc get events --field-selector involvedObject.kind=ComplianceScan,involvedObject.name=workers-scan
LAST SEEN   TYPE     REASON            OBJECT                        MESSAGE
25m         Normal   ResultAvailable   compliancescan/workers-scan   ComplianceScan's result is: NON-COMPLIANT
```

This will also show up in the output of the `oc describe` command.

### The `ComplianceCheckResult` object

When running a scan with a specific profile, several rules in the profiles will
be verified. For each of these rules, we'll have a `ComplianceCheckResult`
object that represents the state of the cluster for a specific rule.

Such an object looks as follows:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceCheckResult
metadata:
  labels:
    compliance.openshift.io/check-severity: medium
    compliance.openshift.io/check-status: FAIL
    compliance.openshift.io/suite: example-compliancesuite
    complianceoperator.openshift.io/scan: workers-scan
  name: workers-scan-no-direct-root-logins
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ComplianceScan
    name: workers-scan
description: |-
  Direct root Logins Not Allowed
  Disabling direct root logins ensures proper accountability and multifactor
  authentication to privileged accounts. Users will first login, then escalate
  to privileged (root) access via su / sudo. This is required for FISMA Low
  and FISMA Moderate systems.
id: xccdf_org.ssgproject.content_rule_no_direct_root_logins
severity: medium
status: FAIL
```

Where:

* **description**: Contains a description of what's being checked and why
  that's being done.
* **id**: Contains a reference to the XCCDF identifier of the rule as it is in
  the data-stream/content.
* **severity**: Describes the severity of the check. The possible values are:
  `unknown`, `info`, `low`, `medium`, `high`
* **status**: Describes the result of the check. The possible values are:
	* **PASS**: Which indicates that check ran to completion and passed.
	* **FAIL**: Which indicates that the check ran to completion and failed.
	* **INFO**: Which indicates that the check ran to completion and found
      something not severe enough to be considered error.
	* **ERROR**: Which indicates that the check ran, but could not complete
      properly.
	* **SKIP**: Which indicates that the check didn't run because it is not
      applicable or not selected.

This object is owned by the scan that created it, as seen in the
`ownerReferences` field.

If a suite is running continuously (having the `schedule` specified) the
result will be updated as needed. For instance, an initial scan could have
determined that a check was failing, the issue was fixed, so a subsequent
scan would report that the check passes.

You can get all the check results from a suite by using the label 
`compliance.openshift.io/suite`

For instance:

```
oc get compliancesuites -l compliance.openshift.io/suite=example-compliancesuite
```

### The `ComplianceRemediation` object

For a specific check, it is possible that the data-stream (content) specified a
possible fix. If a fix applicable to Kubernetes is available, this will create
a `ComplianceRemediation` object, which identifies the object that can be
created to fix the found issue.

A remediation object can look as follows:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
  labels:
    compliance.openshift.io/suite: example-compliancesuite
    complianceoperator.openshift.io/scan: workers-scan
    machineconfiguration.openshift.io/role: worker
  name: workers-scan-disable-users-coredumps
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ComplianceCheckResult
    name: workers-scan-disable-users-coredumps
    uid: <UID>
spec:
  apply: false
  object:
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    spec:
      config:
        ignition:
          version: 2.2.0
        storage:
          files:
          - contents:
              source: data:,%2A%20%20%20%20%20hard%20%20%20core%20%20%20%200
            filesystem: root
            mode: 420
            path: /etc/security/limits.d/75-disable_users_coredumps.conf
```

Where:

* **apply**: Indicates whether the remediation should be applied or not.
* **object**: Contains the definition of the remediation, this object is
  what needs to be created in the cluster in order to fix the issue.

Normally the objects need to be full Kubernetes object definitions, however,
there is a special case for `MachineConfig` objects. These are gathered per
`MachineConfigPool` (which are encompassed by a scan) and are merged into a
single object to avoid many cluster restarts. The compliance suite controller
will also pause the pool while the remediations are gathered in order to give
the remediations time to converge and speed up the remediation process.

This object is owned by the `ComplianceCheckResult` object, as seen in the
`ownerReferences` field.

You can get all the remediations from a suite by using the label 
`compliance.openshift.io/suite`

For instance:

```
oc get complianceremediations -l compliance.openshift.io/suite=example-compliancesuite
```

## Deploying the operator
Before you can actually use the operator, you need to make sure it is
deployed in the cluster.

### Deploying from source
First, become kubeadmin, either with `oc login` or by exporting `KUBECONFIG`.
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


## Using the operator

To run the scans, copy and edit the example file at
`deploy/crds/compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml`
and create the Kubernetes object:
```
# Set this to the namespace you're deploying the operator at
export NAMESPACE=openshift-compliance
# edit the Suite definition to your liking. You can also copy the file and edit the copy.
$ vim deploy/crds/compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml
$ oc create -n $NAMESPACE -f deploy/crds/compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml
```

At this point the operator reconciles the `ComplianceSuite` custom resource,
and creates the `ComplianceScan` objects for the suite. The `ComplianceScan`
then creates scan pods that run on each node in the cluster. The scan
pods execute `openscap-chroot` on every node and eventually report the
results. The scan takes several minutes to complete.

You can watch the scan progress with:
```
$ oc get -n $NAMESPACE compliancesuites -w
```
and even the individual pods with:
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

The operator then aggregates all applied remediations and create a
`MachineConfig` object per scan. This `MachineConfig` object is rendered
to a `MachinePool` and the `MachineConfigDeamon` running on nodes in that
pool pushes the configuration to the nodes and reboots the nodes.

You can watch the node status with:
```
$ oc get nodes
```

Once the nodes reboot, you might want to run another Suite to ensure that
the remediation that you applied previously was no longer found.

## Extracting raw results

The scans provide two kinds of raw results: the full report in the ARF format
and just the list of scan results in the XCCDF format. The ARF reports are,
due to their large size, copied into persistent volumes:
```
oc get pv
NAME                                       CAPACITY  CLAIM
pvc-5d49c852-03a6-4bcd-838b-c7225307c4bb   1Gi       openshift-compliance/workers-scan
pvc-ef68c834-bb6e-4644-926a-8b7a4a180999   1Gi       openshift-compliance/masters-scan

```

An example of extracting ARF results from a scan called `workers-scan` follows:

Once the scan had finished, you'll note that there is a `PersistentVolume` named
after the scan:
```
$ oc get pv
NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM                               STORAGECLASS   REASON   AGE
pvc-577b046a-d791-4b0a-bf03-4dbc5f0f72f1   1Gi        RWO            Delete           Bound    openshift-compliance/workers-scan   gp2                     19m
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
$ oc exec pods/pv-extract ls /workers-scan-results/0
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
$ oc get cm -l=compliance-scan=masters-scan
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
