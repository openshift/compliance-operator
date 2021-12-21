# KubeletConfig Remediation proposed flow design

This page describes the design and implementation of the KubeletConfig 
remediation templating support in the compliance operator.

## Goals
We want to accomplish:
   * The remediation content can have `KubeletConfig` as payload
   * The remediation controller will be able to merge KubeletConfig remediation into the correct KubeletConfig that is currently being used by the cluster

## Add CaC `KubeletConfig` remediation content 
In CaC content, we are going to add `KubeletConfig` remediation besides `MachineConfig` that we currently have.

We will have remediation that has object payload to be `KubeletConfig`. As an example below:
```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
spec:
  kubeletConfig:
    maxPods: 1100
```
## Applying/Unapplying `KubeletConfig` remediation

Applying `KubeletConfig` remediation is just like any other remediation; there
isn't any extra step for it. 

However, things are different when un-applying a `KubeletConfig` remediation.
Our compliance operator does not support un-applying a `KubeletConfig` remediation.
Therefore, if you need to un-apply certain `KubeletConfig` remediation, you will need to 
remove remediation configurations from the corresponding `KubeletConfig` object manually.

## Remediation controller handles `KubeletConfig` remediation

When we are going to apply one of `KubeletConfig` the remediation, the following will happen:

We will apply the `KubeletConfig` remediation to a pool with a matching node selector from the scan.
Since there can only be one `KubeletConfig` per pool, we need to check if the pool
has an existing `KubeletConfig`.

### Remediation when no `KubeletConfig` present for the selected pool

Once we have the selected machine config pool, we need to find out if an Admin has created 
any `KubeletConfig` or our compliance has created any `KubeletConfig` for the pool,
to do that we can check `pool.spec.configuration.source`, if there is only one machine
config for `KubeletConfig` called `name: 01-master-kubelet` in the Machine Config list.

Or we can use a command `oc get KubeletConfig` to see if there is a `KubeletConfig` object that
is in used by any pool.

If there is no `KubeletConfig` for that pool, Compliance Operator create one when
a `KubeletConfig` remediation gets applied. The name of KubeletConfig created by 
the Compliance Operator will be `KubeletConfig compliance-operator-kubelet-<pool-name>`

```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: 
spec:
  machineConfigPoolSelector:
    matchLabels:
      pools.operator.machineconfiguration.openshift.io/master: ""
  kubeletConfig:
    maxPods: 1100
```

### Remediation when there are existing `KubeletConfig` present for the selected pool
If there are more than one `KubeletConfig`, for example `99-master-generated-kubelet`, 
`99-master-generated-kubelet-1`, `99-master-generated-kubelet-2` ..,we need to find the 
last one that was used to render the `MachineConfigPool`'s `MachineConfig`. Only the 
last `KubeletConfig` will be taken into render the final `MachineConfig`


Therefore, we need first to find `99-master-generated-kubelet` with the largest number, in 
this case, it is `99-master-generated-kubelet-4` and then we can find which `KubeletConfig` 
was used to render this MC, in this example:

```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  annotations:
    machineconfiguration.openshift.io/generated-by-controller-version: a537783ea4a0cd3b4fe2a02626ab27887307ea51
  creationTimestamp: "2021-09-20T06:44:07Z"
  generation: 2
  labels:
    machineconfiguration.openshift.io/role: master
  name: 99-master-generated-kubelet-4
  ownerReferences:
  - apiVersion: machineconfiguration.openshift.io/v1
    blockOwnerDeletion: true
    controller: true
    kind: KubeletConfig
    name: set-max-pods-3
    uid: 8decf73a-e49e-474b-982f-ffaebb261894
  resourceVersion: "153110"
  uid: 3cd98e85-6e77-488e-a94b-e84f78ff4029
...
```

From here, we know `KubeletConfig` named `set-max-pods-3` is the one used to 
generate the final pool MC. Next, we need to patch merge the `KubeletConfig`
remediation to this file. We will pause the MC pool and patch all the remediations
one by one, and then unpause the pool once all the remediations have been applied.

```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  annotations:
    machineconfiguration.openshift.io/mc-name-suffix: "4"
  creationTimestamp: "2021-09-20T06:44:07Z"
  finalizers:
  - 99-master-generated-kubelet-4
  generation: 2
  name: set-max-pods-3
  resourceVersion: "158917"
  uid: 8decf73a-e49e-474b-982f-ffaebb261894
spec:
  kubeletConfig:
    maxPods: 901
  machineConfigPoolSelector:
    matchLabels:
      custom-kubelet: kubelet-set-max-pods
status:
  conditions:
  - lastTransitionTime: "2021-09-20T06:53:22Z"
    message: Success
    status: "True"
    type: Success
```
