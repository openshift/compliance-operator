# KubeletConfig Remediation proposed flow design

This page describes the design and implementation of the KubeletConfig 
remediation templating support in the compliance operator.

## Goals
We want to accomplish:
   * The remediation content can have `KubeletConfig` as payload
   * The remediation controller will be able to merge KubeletConfig remediation into the correct KubeletConfig that is currently being used by the cluster

## Add CaC `KubletConfig` remediation content 
In CaC content, we are going to add `KubletConfig` remediation besides `MachineConfig` that we currently have.

We will have remdiation that has object payload to be `KubletConfig`. As a exmaple below:
```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
spec:
  kubeletConfig:
    maxPods: 1100
```

## Remediation controller handles `KubletConfig` remediation

When we are going to apply one of `KubletConfig` remediation the following will happen:

First, we need to get MachineConfigPool list.
```go
mcfgpools := &mcfgv1.MachineConfigPoolList{}
  if err := r.client.List(context.TODO(), mcfgpools); err != nil {
    return fmt.Errorf("couldn't list the pools for the remediation: %w", err)
  }
```

We then find the poola that meets the remedition nodes' label. ex. worker/master 
nodes `machineconfiguration.openshift.io/role:`

We will use the nodeselector: `machineconfiguration.openshift.io/role:`  to match 
machineConfigPoolSelector: `pools.operator.machineconfiguration.openshift.io/:`

### Remediation when no `KubeletConfigs` present

Once we have the machine config pool, we need to find out if an Admin has created 
any `KubeletConfigs`, to do that we can check `pool.spec.configuration.source`, if 
there is only one `name: 01-master-kubelet` machine config in the list.

If there is no `KubeletConfigs` for that pool, we will need to create one, and also 
add a label to the pool depends on the nodeselctor `pools.operator.machineconfiguration.openshift.io/master: ""`/ `pools.operator.machineconfiguration.openshift.io/worker: ""`.: The name of KubeletConfig will be the scan name.

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

### Remediation when there are custom `KubeletConfigs` present
If there are more than one `KubeletConfigs`, for example `99-master-generated-kubelet`, 
`99-master-generated-kubelet-1`, `99-master-generated-kubelet-2` ..,we need to find the 
last one that was used to render the `MachineConfigPool`'s `MachineConfig`. Only the 
last `KubeletConfigs` will be taken into render the final `MachineConfig`


Therefore, we need first to find `99-master-generated-kubelet` with largest number, in 
this case  it is `99-master-generated-kubelet-4` and then we can find which `KubletConfig` 
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
generate the final pool MC. Next, we need to patch merge the compliance remediation 
kubelet config remediation to this file. We will pause the MC pool and start 
the patch one by one untill all the remediations have been applied.

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
