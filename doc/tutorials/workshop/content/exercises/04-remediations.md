---
Title: Working with Remediations
PrevPage: 03-creating-your-first-scan
NextPage: 05-tailoring-profiles
---
Working with Remediations
=========================

Remediations are an important part of the compliance process. They fill in the technical gaps in your
deployment, and when addressed, get you closer to compliance with the benchmark you need to comply to.

Exploring the remediations
--------------------------

As you noticed in the previous section, the compliance operator already generated some remediations
for you. They are accessible as any other Kubernetes object.

You can even inspect them as other objects via `oc explain complianceremediation`. e.g. to see
an explanation on the `spec` section, you can do `oc explain complianceremediation.spec`.

To list all the remediations that were generated as part of a specific suite run, you can do:

```
oc get complianceremediations -l compliance.openshift.io/suite=<suite name>
```

Let's see the remediations that came from the scan we did in the previous exercise

```
$ oc get complianceremediations -l compliance.openshift.io/suite=periodic-e8
NAME                                                       STATE
rhcos4-e8-master-audit-rules-dac-modification-chmod        NotApplied
rhcos4-e8-master-audit-rules-dac-modification-chown        NotApplied
rhcos4-e8-master-audit-rules-execution-chcon               NotApplied
rhcos4-e8-master-audit-rules-execution-restorecon          NotApplied
rhcos4-e8-master-audit-rules-execution-semanage            NotApplied
rhcos4-e8-master-audit-rules-execution-setfiles            NotApplied
...
```

You'll notice that none of the remediations are applied. This is the default behavior, as
we don't want the operator unexpectedly changing the cluster's configuration.

Let's inspect one of the generated remediations and see what it does:

```
$ oc get complianceremediation rhcos4-e8-worker-sysctl-kernel-dmesg-restrict -o yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
  creationTimestamp: "2020-09-08T11:47:09Z"
  generation: 1
  labels:
    compliance.openshift.io/scan-name: rhcos4-e8-worker
    compliance.openshift.io/suite: periodic-e8
    machineconfiguration.openshift.io/role: worker
  name: rhcos4-e8-worker-sysctl-kernel-dmesg-restrict
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ComplianceCheckResult
    name: rhcos4-e8-worker-sysctl-kernel-dmesg-restrict
    uid: 83aed9ae-2f08-4a30-a850-368385d1d00a
  resourceVersion: "28256"
  selfLink: /apis/compliance.openshift.io/v1alpha1/namespaces/openshift-compliance/complianceremediations/rhcos4-e8-worker-sysctl-kernel-dmesg-restrict
  uid: cb8e972a-4257-47fb-817d-db60b1fd20bc
spec:
  apply: true
  current:
    object:
      apiVersion: machineconfiguration.openshift.io/v1
      kind: MachineConfig
      spec:
        config:
          ignition:
            version: 3.1.0
          storage:
            files:
            - contents:
                source: data:,kernel.dmesg_restrict%3D1%0A
              mode: 420
              overwrite: true
              path: /etc/sysctl.d/75-sysctl_kernel_dmesg_restrict.conf
  outdated: {}
status:
  applicationState: NotApplied
```

You'll notice that this remediation merely creates a MachineConfig object that sets the `dmesg_restrict` 
sysctl. You'll also notice that there is a controller set in the `ownerReferences` key in the metadata
of the remediation. This object points to a **ComplianceCheckResult** object which contains further
explanations about what was checked and why this remediation needs to be applied. We already examined
the results in the previous chapter, so for this exercise, let's just just see what the check is about:

```
$ oc get compliancecheckresult rhcos4-e8-worker-sysctl-kernel-dmesg-restrict -o jsonpath="{.description}"
Restrict Access to Kernel Message Buffer
Unprivileged access to the kernel syslog can expose sensitive kernel
address information.
```

While this information is relevant, we might want to know more. This is where it's relevant to check the
rule that this remediation came from. Unfortunately, the rule names could get quite long; Hence why these
object references are stored as annotations. So, let's get the relevant annotation and use that to search for the relevant rule:

```
$ oc get compliancecheckresult rhcos4-e8-worker-sysctl-kernel-dmesg-restrict -o json | jq -r ".metadata.annotations"
{"compliance.openshift.io/rule":"sysctl-kernel-dmesg-restrict"}
```

Given that we used the rhcos4 **ProfileBundle**, we can search the rules from there:

```
$ oc get rules.compliance -l compliance.openshift.io/profile-bundle=rhcos4 | \
    grep sysctl-kernel-dmesg-restrict
rhcos4-sysctl-kernel-dmesg-restrict
```

Normally, the rule annotations match the name of the rule objects, though the rule objects have the
**ProfileBundle** name prepended. With this object, we can now check either the description, the
rationale, or merely the title to see what the rule does and why it does it. We can also see the relevant
security controls that are being addressed by browsing the annotations:

```
$ oc get rules.compliance rhcos4-sysctl-kernel-dmesg-restrict -o jsonpath="{.metadata.annotations}" | jq
{
  "compliance.openshift.io/image-digest": "pb-rhcos4jbl7l",
  "compliance.openshift.io/rule": "sysctl-kernel-dmesg-restrict",
  "control.compliance.openshift.io/NIST-800-53": "SI-11(a);SI-11(b)",
  "policies.open-cluster-management.io/controls": "SI-11(a),SI-11(b)",
  "policies.open-cluster-management.io/standards": "NIST-800-53"
}
```

Applying a remediation
----------------------

Now that we know what the remediation does and why it was suggested, let's apply it!

Applying a remediation can be done by the admin manually. This means that you would need to download
the remediation object and apply the object.

However, it's also possible to let the compliance-operator do this for you. This is what the `apply` flag
is for. When setting this to true, the compliance-operator will detect this and create the object in
OpenShift.

Let's apply the aforementioned remediation:

```
$ oc patch complianceremediations rhcos4-e8-worker-sysctl-kernel-dmesg-restrict \
    -p '{"spec":{"apply":true}}' --type=merge
complianceremediation.compliance.openshift.io/rhcos4-e8-worker-sysctl-kernel-dmesg-restrict patched
```

We can verify that the compliance-operator has detected this change by re-fetching the remediation object:

```
$ oc get complianceremediations rhcos4-e8-worker-sysctl-kernel-dmesg-restrict 
NAME                                            STATE
rhcos4-e8-worker-sysctl-kernel-dmesg-restrict   Applied
```

So... What's going on?

Given that the remediation's object is a **MachineConfig** object, the compliance-operator will
create this object in the cluster. Let's take a look at what the operator did:

```
$ oc get machineconfigs
NAME                                               GENERATEDBYCONTROLLER                      IGNITIONVERSION   AGE
00-master                                          522f0fa36cc7b952c6e98e120c58c66e6d795544   3.1.0             136m
...
75-rhcos4-e8-worker-sysctl-kernel-dmesg-restrict                                              3.1.0             2m
...
rendered-worker-d9660384409dbfe5b05ff543c69ef81a   522f0fa36cc7b952c6e98e120c58c66e6d795544   3.1.0             115s
```

The output has been trimmed for readability. But the main thing to note is that a MachineConfig called
`75-rhcos4-e8-worker-sysctl-kernel-dmesg-restrict` was created. If we inspect it we'll notice that it has the same
contents as the remediation that we just applied.

```
$ oc get machineconfig 75-rhcos4-e8-worker-sysctl-kernel-dmesg-restrict -o yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  annotations:
    compliance.openshift.io/remediation: ""
  creationTimestamp: "2021-02-16T18:54:43Z"
  generation: 1
  labels:
    compliance.openshift.io/scan-name: rhcos4-e8-worker
    compliance.openshift.io/suite: periodic-e8
    machineconfiguration.openshift.io/role: worker
  managedFields:
...
  name: 75-rhcos4-e8-worker-sysctl-kernel-dmesg-restrict
  resourceVersion: "136045"
  selfLink: /apis/machineconfiguration.openshift.io/v1/machineconfigs/75-rhcos4-e8-worker-sysctl-kernel-dmesg-restrict
  uid: 267bda7e-b170-4817-b9f5-89047e7888f5
spec:
  config:
    ignition:
      version: 3.1.0
    storage:
      files:
      - contents:
          source: data:,kernel.dmesg_restrict%3D1%0A
        mode: 420
        overwrite: true
        path: /etc/sysctl.d/75-sysctl_kernel_dmesg_restrict.conf
```

Now we can just let the **machine-config-operator** do its job and apply this configuration
to all of the hosts whose **MachineConfigPool** matches the `worker` role.

You might have noticed that the **machine-config-operator** did a rolling restart of the nodes in
order to apply the configuration. This is normal and expected. However, this might be problematic
if you're applying several MachineConfig object changes, as each change will trigger a restart of
all the nodes in that pool.

Let's wait for the `worker` **MachineConfigPool** to finish updating before continuing to the next
section. You can see the progress by doing:

```
$ oc get mcp -w
``` 

The final output is that the relevant pool should end up with the `UPDATED` field equal to `True`.

Applying multiple remediations
------------------------------

The compliance-operator will only create one **MachineConfig** object per **ComplianceSuite**, even if
several remediation are applied. However, each remediation application will modify this object, and thus
a restart will happen.

To avoid this, let's pause the relevant **MachineConfigPool** (in this case, the `worker` pool).

```
$ oc patch machineconfigpools worker -p '{"spec":{"paused":true}}' --type=merge
machineconfigpool.machineconfiguration.openshift.io/worker patched
```

We can verify that the pool is indeed paused with the following command:

```
$ oc get machineconfigpools worker -o yaml | grep "\spaused"
  paused: true
```

Now that the pool is paused, we can freely apply a couple of remediations and be sure that there won't
be any unnecessary node reboots.

Let's do so!

```
$ oc patch complianceremediations rhcos4-e8-worker-sysctl-kernel-randomize-va-space \
    -p '{"spec":{"apply":true}}' --type=merge
complianceremediation.compliance.openshift.io/rhcos4-e8-worker-sysctl-kernel-randomize-va-space patched
$ oc patch complianceremediations rhcos4-e8-worker-sysctl-kernel-unprivileged-bpf-disabled \
    -p '{"spec":{"apply":true}}' --type=merge
complianceremediation.compliance.openshift.io/rhcos4-e8-worker-sysctl-kernel-unprivileged-bpf-disabled patched
```
Now we un-pause the pool and let the machine-config-operator persist the changes.

```
$ oc patch machineconfigpools worker -p '{"spec":{"paused":false}}' --type=merge
machineconfigpool.machineconfiguration.openshift.io/worker patched
```

### To what *MachineConfigPool* will my remediations be applied to?

You might have noticed that in this case that these remediations applied to only the `worker`
**MachineConfigPool**. This is because the **ComplianceSuite** created two **ComplianceScan** objects
under the hood. These scans have a specific `nodeSelector` set that should match a **MachineConfigPool**. 
This way we ensure that the remediations are applicable to nodes with similar configurations, thus 
reducing inconsistencies and helping us keep the remediations applicable. This was all abstracted
away thanks to the `roles` key in the **ScanSettings** object.

Letting the operator apply the remediations
-------------------------------------------

Applying each scan can be fairly tedious. This is why the compliance-operator has a flag for this!


Applying scans is done in a suite level, which means it's done for all the scans that belong to the 
benchmark you're currently scanning. It is recommended you only do this once the remediations have
been properly audited and evaluated.

To recap the previous section quickly:

We created a **ScanSettingBinding** object called `periodic-e8` which does a *Platform* scan with the e8 
profile, and a *Node* with the e8 profile as well. Under the hood, this created one **ComplianceSuite** 
object called `periodic-e8` (as the parent **ScanSettingBinding**), which contains three **ComplianceScans**:

* ocp4-e8
* rhcos4-e8-master
* rhcos4-e8-worker

Each of this scans will yield results and remediations. By setting the `autoApplyRemediations` flag,
all of them will be applied.

Let's do this!

We'll modify the **ScanSettings** object we used for the binding:

```
$ oc patch scansettings periodic-setting -p '{"autoApplyRemediations":true}' --type=merge
scansetting.compliance.openshift.io/periodic-setting patched
```

This will go ahead and set all the **ComplianceRemediations** belonging to the suite to be applied.

We can verify this as follows:

```
$ oc get complianceremediations -l compliance.openshift.io/suite=periodic-e8
NAME                                                       STATE
rhcos4-e8-master-audit-rules-dac-modification-chmod        Applied
rhcos4-e8-master-audit-rules-dac-modification-chown        Applied
rhcos4-e8-master-audit-rules-execution-chcon               Applied
rhcos4-e8-master-audit-rules-execution-restorecon          Applied
rhcos4-e8-master-audit-rules-execution-semanage            Applied
rhcos4-e8-master-audit-rules-execution-setfiles            Applied
rhcos4-e8-master-audit-rules-execution-setsebool           Applied
rhcos4-e8-master-audit-rules-execution-seunshare           Applied
...
rhcos4-e8-worker-audit-rules-dac-modification-chmod        Applied
rhcos4-e8-worker-audit-rules-dac-modification-chown        Applied
rhcos4-e8-worker-audit-rules-execution-chcon               Applied
rhcos4-e8-worker-audit-rules-execution-restorecon          Applied
rhcos4-e8-worker-audit-rules-execution-semanage            Applied
rhcos4-e8-worker-audit-rules-execution-setfiles            Applied
rhcos4-e8-worker-audit-rules-execution-setsebool           Applied
rhcos4-e8-worker-audit-rules-execution-seunshare           Applied
rhcos4-e8-worker-audit-rules-login-events-faillock         Applied
rhcos4-e8-worker-audit-rules-login-events-lastlog          Applied
rhcos4-e8-worker-audit-rules-login-events-tallylog         Applied
rhcos4-e8-worker-audit-rules-networkconfig-modification    Applied
rhcos4-e8-worker-audit-rules-usergroup-modification        Applied
rhcos4-e8-worker-auditd-name-format                        Applied
...
```

You'll now note that there is a **MachineConfig** object per *Node* scan:

```
$ oc get machineconfig
NAME                                               GENERATEDBYCONTROLLER                      IGNITIONVERSION   AGE
...
75-rhcos4-e8-master-periodic-e8                                                               2.2.0             2m43s
75-rhcos4-e8-worker-periodic-e8                                                               2.2.0             2m43s
...
```

Under the hood, this automated the process of applying these remediations. The compliance-operator
paused the relevant **MachineConfigPools**, converged the remediations to the relevant **MachineConfig**
objects, and finally un-paused the pools.

We can now wait for the **machine-config-operator** to converge the status of the nodes by rebooting and
applying the configurations.

***

Let's now move to the next section, which teaches us how to [tailor our profiles](05-tailoring-profiles.md)
