## Remediation flow design
This page describes the design and implementation of the remediation
support in the compliance operator.

### Goals
We want to accomplish:
   * It is possible to scan the cluster for gaps in compliance
        * This scan or scans would detect gaps in compliance
   * The administrator is then able to review the remediations for the
     gaps in compliance
        * Some remediations would be fully automated, others would just provide
          templates or guidance.
   * Those remediations the administrator selects for applying would
     be automatically applied by the operator
        * For the node-level remediations, this would happen by creating a
          `MachineConfig` object
        * Cluster-level remediations will be implemented as a next step.
          The basic idea is to have another container that calls out to
          the kubernetes and/or OpenShift APIs, fetches the needed artifacts
          as JSON objects and then lets another OpenScap instance scan
          the JSON objects.
   * After the remediations are applied, the scan is re-run to reflect
     the updated state of the cluster
   * The operator would also watch for API resources that affect compliance
     (such as `MachineConfigs`) and re-run the scan if those are updated

### High-level overview
An OCP cluster consists of the Kubernetes engine running on nodes. From
the compliance scan perspective, we would be scanning the nodes and the
Kubernetes separately, because the nodes and the cluster would be scanned
using a different compliance content and even a different scanner
(`oscap-chroot` for the nodes' FS mounted in a volume, `oscap` for the
cluster-level checks where the scanner would scan JSON artifacts gathered
from the cluster using k8s or OpenShift API calls)

The compliance of the cluster as a whole would be represented by an instance
of a CR `ComplianceSuite`. A scan of either the nodes of the nodes or
the cluster would be represented by a CR `ComplianceScan`, owned by the
`ComplianceSuite`.  For each of the gaps, a `ComplianceRemediation` resource
carrying the actual remediation payload would be created. The administrator then
reviews the remediations, selects those that should be applied by changing
a value of the `apply` field. At that point, the remediation is picked up
by the `complianceremediation` controller which merges all the applied
remediations into a single per-scan `MachineConfig` object which is finally
applied to the nodes by the MCO.

There might be multiple node-level scans, because the cluster might consist
of different OSs, for example RHCOS for the master nodes and RHEL for the
worker nodes.

The following two YAML examples are real CRs retrieved with `oc get -oyaml`,
just with the metadata trimmed. The structure looks like this:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceSuite
metadata:
  name: example-compliancesuite
  namespace: openshift-compliance
spec:
  autoApplyRemediations: false
  scans:
  - content: ssg-rhcos4-ds.xml
    name: workers-scan
    nodeSelector:
      node-role.kubernetes.io/worker: ""
    profile: xccdf_org.ssgproject.content_profile_coreos-ncp
  - content: ssg-rhcos4-ds.xml
    name: masters-scan
    nodeSelector:
      node-role.kubernetes.io/master: ""
    profile: xccdf_org.ssgproject.content_profile_coreos-ncp
status:
  Phase: DONE
  Result: NON-COMPLIANT
  scanStatuses:
  - name: workers-scan
    phase: DONE
    result: NON-COMPLIANT
  - name: masters-scan
    phase: DONE
    result: NON-COMPLIANT
```

The remediation looks like this:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
  labels:
    compliance.openshift.io/suite: example-compliancesuite
    compliance.openshift.io/scan-name: masters-scan
    machineconfiguration.openshift.io/role: master
  name: masters-scan-no-direct-root-logins
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ComplianceSuite
    name: example-compliancesuite
spec:
  apply: false
  object:
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    spec:
      fips: false
      osImageURL: ""
      kernelArguments:
        - ""
      config:
        ignition:
          version: 2.2.0
        storage:
          files:
          - contents:
              source: data:,
            filesystem: root
            mode: 0600
            path: /etc/securetty
```

### The scan-remediate-repeat flow
The general flow is common for the platform scan as well as the node
scan.  How the remediations are represented and therefore how they are applied
differs for node and platform scans.

Coming from the administrator side, the admin would define the
`ComplianceSuite` CR and add the scans. First, the `ComplianceScan` CR
would be validated in the `pending` phase before the scan launches.  Then the
scan executes and `openscap` produces its report. The scanner must parse the
report and for each gap the scan identified create a `ComplianceRemediation`
CR. The `ComplianceRemediation` CR would be owned by the `ComplianceSuite`
so that if the suite gets deleted, the remediations would be as well.

The `ComplianceRemediation` objects would link back to the suite with
labels that identify the suite and the scan respectively. This way, the
administrator would be able to get and inspect the remediations with the
usual `oc` command, e.g.
`oc get complianceremediations --selector compliance.openshift.io/suite=example-compliancesuite`.

Once the administrator reviews the remediations, they would set the `apply`
field to `true`. At that point, the remediation controller would pick up
the remediation and apply it.

#### Applying node-level remediations
The remediation controller takes all the `ComplianceRemediation` objects that
are applicable (`apply: true`) and merges it into a single `MachineConfig`
object per scan. Because the `MachineConfig` objects are applied to
`MachinePool` objects with the help of labels, there needs to be a 1:1
mapping between the `ComplianceScan` resource and the `MachinePool` resource.

The merged remediation then would be created, replacing any previous
remediation that might have existed from a previous compliance scan run.
When a new `MachineConfig` is created, the `machine-config-operator` renders
the resulting per-pool machineconfig objects by combining all the applicable
`MachinecConfigs` and passes the rendered result to the `machine-config-deamon`
running on the nodes that reboot and apply the rendered configuration. At this
point, the scan results are no longer valid and the scan needs to be re-ran to
asses compliance again.

For the node scans, we could re-run the scan when a `MachineConfigPool` finishes
updating, e.g. the pool is running the config rendered after our `MachineConfig`
was updates and its state is `Updated: True`. In more detail, the `compliance-controller`
would watch for the `MachineConfigPool` and reset the scan status when to `pending`
then the `MachineConfigPool` is updating and then launch the scan again then the
pool finishes updating.

#### Applying cluster-level remediations
TBD

### Detecting changes that break compliance
The cluster administrator might inadvertently change the cluster configuration
and make the cluster non-compliant. The compliance operator should try to detect
this and re-run the scans to be able to proactively warn about getting out of
compliance.

#### On the node level
Because the nodes should pretty much only be configured using `MachineConfig`
resources, the `compliance-controller` could watch for `MachineConfig` objects
and re-run the scans.

TODO: Optimize the scans based on the pool the MC is applied to or just re-run
the whole thing?

#### Working with remediations
Run the operator first, then create the `ComplianceSuite` CR:

    oc create -f deploy/crds/compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml

You'll be able to watch the Suite:

    oc describe compliancesuites/example-compliancesuite

Eventually the scan will finish and should find some issues:

```
Status:
  Aggregated Phase:   DONE
  Aggregated Result:  NON-COMPLIANT
  Scan Statuses:
    Name:    workers-scan
    Phase:   DONE
    Result:  NON-COMPLIANT
```

We can fetch the generated remediations as follows:

```
$ oc get complianceremediations --selector compliance.openshift.io/suite=example-compliancesuite

NAME                                                             STATE
workers-scan-auditd-name-format                                  NotApplied
workers-scan-coredump-disable-backtraces                         NotApplied
workers-scan-coredump-disable-storage                            NotApplied
workers-scan-disable-ctrlaltdel-burstaction                      NotApplied
workers-scan-disable-users-coredumps                             NotApplied
workers-scan-grub2-audit-argument                                NotApplied
workers-scan-grub2-audit-backlog-limit-argument                  NotApplied
workers-scan-grub2-page-poison-argument                          NotApplied
workers-scan-grub2-pti-argument                                  NotApplied
workers-scan-kernel-module-atm-disabled                          NotApplied
workers-scan-kernel-module-bluetooth-disabled                    NotApplied
workers-scan-kernel-module-can-disabled                          NotApplied
workers-scan-kernel-module-cramfs-disabled                       NotApplied
workers-scan-kernel-module-firewire-core-disabled                NotApplied
...
```
Because remediations are not applied automatically, you'll want to edit the remediation
and apply it yourself:

    oc edit complianceremediations/workers-scan-no-direct-root-logins

Would open an editor that will contain (simplified):
```yaml
spec:
  # Change to true
  apply: false
  object:
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    metadata:
      labels:
        machineconfiguration.openshift.io/role: worker
      name: 50-worker-empty-securetty
```
After you change `apply` to `true`, the `Remediations`  controller picks up
the remediation, reads the MC out of it and applies the remediation as a merged MC.
View the created MC with:

    oc describe machineconfigs/75-workers-scan-example-compliancesuite

Try also applying the other remediation:

    oc edit complianceremediations/workers-scan-no-empty-passwords

And then view the merged MC again:

    oc describe machineconfigs/75-workers-scan-example-compliancesuite

You'll see that both remediations were merged into a single one. Now you'll probably
want to wait until the MCs are applied and the nodes rebooted. Afterwards, delete
the suite and start it again. You'll see that the checks that were previously failing
are now passing and no new remediations are being proposed.
