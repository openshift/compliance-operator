# Remediation flow design
This page describes the design and implementation of the remediation
support in the compliance operator.

## Goals
We want to accomplish:
   * It is possible to scan the cluster for gaps in compliance
   * The results of the scan are represented as `ComplianceCheckResult` CRs,
     with an appropriate state (`fail`) that represents gaps in compliance
   * The administrator is then able to review the `ComplianceCheckResult` objects
     to view the scan results
   * For those findings that can be remediated automatically, a `ComplianceRemediation`
     object is created
   * Findings that cannot be remediated automatically will include steps to
     remediate in the `ComplianceCheckResult` object itself
   * Those remediations the administrator selects for applying would
     be automatically applied by the operator
        * In general the remediations are generic Kubernetes objects, although
          for Node-type scans, `MachineConfig` objects are commonly used
   * After the remediations are applied, the scan can be re-run to reflect
     the updated state of the cluster

## High-level overview
An OCP cluster consists of the Kubernetes engine running on nodes. From
the compliance scan perspective, we would be scanning the nodes and the
Kubernetes platform separately, because the nodes and the cluster would be scanned
using a different compliance content and even a different scanner
(`oscap-chroot` for the nodes' host FS mounted in a volume, `oscap` for the
cluster-level checks where the scanner would scan JSON artifacts gathered
from the cluster using k8s or OpenShift API calls)

The compliance of the cluster as a whole would be represented by an instance
of a CR `ComplianceSuite`. A scan of either the nodes of the nodes or
the cluster would be represented by a CR `ComplianceScan`, owned by the
`ComplianceSuite`.  For each test in a `ComplianceScan`, a `ComplianceCheckResult`
would be created, with metadata about the test such as result (pass/fail), severity
or manual steps to test or remediate. Finally, for each of gaps that can be remediated
automatically, a `ComplianceRemediation` resource
carrying the actual remediation payload would be created. The administrator then
reviews the remediations, selects those that should be applied by changing
a value of the `apply` field. At that point, the remediation is picked up
by the `complianceremediation` and if the remediation can be applied, a Kubernetes
object (or a more specialized `MachineConfig`) object is created.

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
    scanType: Node
    nodeSelector:
      node-role.kubernetes.io/worker: ""
    profile: xccdf_org.ssgproject.content_profile_moderate
  - content: ssg-rhcos4-ds.xml
    name: masters-scan
    scanType: Node
    nodeSelector:
      node-role.kubernetes.io/master: ""
    profile: xccdf_org.ssgproject.content_profile_moderate
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
  current:
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
            version: 3.1.0
          storage:
            files:
            - contents:
                source: data:,
              filesystem: root
              mode: 0600
              path: /etc/securetty
    outdated: {} 
```

### The scan-remediate-repeat flow
The general flow is common for the platform scan as well as the node
scan.  How the remediations are represented and therefore how they are applied
differs for node and platform scans. For more details on the flow, please
refer to the (troubleshooting document)[troubleshooting.md].

Coming from the administrator side, the admin would define the
`ScanSettings` and `ScanSettingBindings` which generate a `ComplianceSuite`
that itself unrolls into one or more `ComplianceScan` objects.
After the scans finish, `ComplianceCheckResult` objects are generated for
each test in the scan and a `ComplianceRemediation` object for every gap
that can be remediated automatically.

The `ComplianceRemediation` objects would link back to the `suite` with
labels that identify the suite and the scan respectively. This way, the
administrator would be able to get and inspect the remediations with the
usual `oc` command, e.g.
`oc get complianceremediations --selector compliance.openshift.io/suite=example-compliancesuite`.

Just when the `ComplianceRemediation` is created, its status is set to `Pending`.
By default, when the remediation is not applied yet, the `complianceremediationcontroller`
would set the remediation state to `NotApplied`.
Once the administrator reviews the remediations, they would set the `apply`
field to `true`. At that point, the remediation controller would pick up
the remediation and apply it, changing its state to `Applied`.

#### Applying node remediations
The remediation controller takes all the `ComplianceRemediation` objects that
are applicable (`apply: true`) and creates a Kubernetes object (for Node-level
checks, usually a `MachineConfig` object whose name starts with `75-`)
). Because the `MachineConfig` objects are applied to
`MachinePool` objects with the help of labels, there needs to be a 1:1
mapping between the `ComplianceScan` resource and the `MachinePool` resource.

The remediation payload then would be created, replacing any previous
remediation that might have existed from a previous compliance scan run.
When a new `MachineConfig` is created, the `machine-config-operator` renders
the resulting per-pool `MachineConfig` objects by combining all the applicable
`MachineConfigs` and passes the rendered result to the `machine-config-deamon`
running on the nodes that reboot and apply the rendered configuration. At this
point, the scan results are no longer valid and the scan needs to be re-ran to
asses compliance again.

For the node scans, we could re-run the scan when a `MachineConfigPool` finishes
updating, e.g. the pool is running the config rendered after our `MachineConfig`
was updated and its state is `Updated: True`. In more detail, the `compliance-controller`
would watch for the `MachineConfigPool` and reset the scan status when to `pending`
then the `MachineConfigPool` is updating and then launch the scan again then the
pool finishes updating.

#### Applying platform remediations
Same as Node remediations, just flip the `apply` attribute to `true`. Since platform
remediations are often generic Kubernetes objects like `ConfigMaps`, no reboot is typically
required and once the remediation status changes to `Applied`, you can re-run the scan
to view the updated results.

### Working with remediations

#### Applying one remediation

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
  current:
    object:
      apiVersion: machineconfiguration.openshift.io/v1
      kind: MachineConfig
      metadata:
        labels:
          machineconfiguration.openshift.io/role: worker
        name: 50-worker-empty-securetty
```
After you change `apply` to `true`, the `Remediations`  controller picks up
the remediation, reads the object out of it and applies the remediation.

View the created MC with:

    oc describe machineconfigs/75-upstream-rhcos4-moderate-worker-no-direct-root-logins

#### Applying multiple remediations

It's possible to apply all of the remediations generated by a `ComplianceSuite` object
in one go. To do this, one can simply annotate the `ComplianceSuite` as follows:

```
oc annotate compliancesuites/$SUITE_NAME compliance.openshift.io/apply-remediations=
```

This will iterate through all of the remediations generated by a `ComplianceSuite` and
apply them. Note that this will only happen once the Suite is in the `Done` phase.

#### Applying remediations automatically

It's possible to tell the Compliance Operator that, if a remediation is created in a suite,
it should attempt to apply it. This is done through the `autoApplyRemediations` flag which is
available in both the `ScanSettings` and the `ComplianceSuite` itself. If this is enabled,
the operator will apply the remediations belonging to a scan once the ComplianceSuite reaches
the `Done` phase. To avoid multiple reboots, the Compliance Operator would pause the 

#### Remediations with dependencies

Some remediations might not be applied right away, but there are some remediations that require that a
rule or check passes before applying.. Those are annotated with `compliance.openshift.io/depends-on`.
An example of such remediation follows:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
  annotations:
    compliance.openshift.io/depends-on: xccdf_org.ssgproject.content_rule_package_usbguard_installed
...
```

Note that the dependencies are stored in the rule definitions in the
[ComplianceAsCode](https://github.com/ComplianceAsCode/content)
repository, and the kubernetes remediations should have an annotation
called `complianceascode.io/depends-on` if a dependency needs to be expressed.

Note that the `depends-on` annotation will contain the XCCDF ID from the rules that are expected
to pass in order for the operator to apply the remediation. The dependency uses the XCCDF ID because
this comes from the compliance content itself, and this is a constant that is available in that 
context. The operator itself will detect this dependency, get the ComplianceCheckResult that
with that ID, and will only apply the remediation if the rule passes. `FAIL` or an `INFO` state
will not apply the rule.

If this `ComplianceRemediation` is selected to be applied, but the dependencies
cannot be resolved, the `ComplianceRemediation` object transitions into the
`MissingDependencies` state. The operator will also add a label a the key of
`compliance.openshift.io/has-unmet-dependencies` to help users filter remediations with dependencies
in an easier manner. What dependencies are missing is then visible
as events attached to the `ComplianceRemediation` object. Apply those remediation
first and then re-run the scan to resolve the missing dependencies.

A subsequent run of the `ComplianceSuite` will re-trigger the remediation's reconcile loop, and if the
remediation's dependencies are met, the operator will finally apply and the object will be created.
