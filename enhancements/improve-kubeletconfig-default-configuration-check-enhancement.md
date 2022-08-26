---
title: kubeletconfig-default-configuration-check
authors:
  - Vincent056
reviewers: # Include a comment about what domain expertise a reviewer is expected to bring and what area of the enhancement you expect them to focus on. For example: - "@networkguru, for networking aspects, please look at IP bootstrapping aspect"
  - TBD
approvers:
  - TBD
api-approvers: # In case of new or modified APIs or API extensions (CRDs, aggregated apiservers, webhooks, finalizers). If there is no API change, use "None"
  - TBD
creation-date: 2022-07-15
last-updated: 2022-07-15
tracking-link: # link to the tracking ticket (for example: Jira Feature or Epic ticket) that corresponds to this enhancement
  - TBD
---

# KubeletConfig Default Configuration Check

## Summary

This will allow us to improve `KubeletConfig` checks by adding the ability to
check the default configuration of the `KubeletConfig`. We're aggregating the
configs into a single place to make it easier to write content against the
`KubeletConfig`.

## Motivation

Currently, all the `KubeletConfig` checks fails unless they are explicitly
set by the user. And to set the `KubeletConfig`, it will trigger a node reboot. If
we enable Compliance Operator to check the default configuration of the `KubeletConfig`,
a reboot might be avoided when the default configuration meets the compliance requirements.


### Goals

* Compliance Operator will be able to check the default configuration 
of the `KubeletConfig`.

* All the KubeletConfig rules needs to be updated to work with this enhancement.

## Proposal

In `cmd/manager/scap.go`, we will fetch the name of each node.

```go
func fetchNodes(ctx context.Context, c runtimeclient.Client) ([]string, error) {
	nodeList := v1.NodeList{}
	if err := c.List(ctx, &nodeList); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}
	nodes := make([]string, len(nodeList.Items))
	for i, node := range nodeList.Items {
		nodes[i] = node.Name
	}
	return nodes, nil
}
```

then we use the name of each node to fetch the `KubeletConfig` of each node

```go
nodesName, err := fetchNodes(context.TODO(), c.client)

if err != nil {
	return err
}

for i, nodeName := range nodesName {
	found = append(found, utils.ResourcePath{
		ObjPath:  fmt.Sprintf("/api/v1/nodes/%s/proxy/configz", nodeName),
		DumpPath: fmt.Sprintf("/api/v1/nodes/%s/proxy/configz", strconv.Itoa(i)),
		Filter:   `.kubeletconfig|.kind="KubeletConfiguration"|.apiVersion="kubelet.config.k8s.io/v1beta1"`,
	})
}
```

example result
```json
{
  "tlsCipherSuites": [
    "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
    "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
    "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
    "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
    "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
    "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256"
  ],
  "tlsMinVersion": "VersionTLS12",
  "rotateCertificates": true,
  "serverTLSBootstrap": true,
  "registryPullQPS": 5,
  "registryBurst": 10,
  "eventRecordQPS": 5,
  "eventBurst": 10,
  "enableDebuggingHandlers": true,
  "streamingConnectionIdleTimeout": "4h0m0s",
  "cpuManagerReconcilePeriod": "10s",
  "memoryManagerPolicy": "None",
  "topologyManagerPolicy": "none",
  "topologyManagerScope": "container",
  "runtimeRequestTimeout": "2m0s",
  "hairpinMode": "promiscuous-bridge",
  "maxPods": 250,
  "podPidsLimit": 4096,
  "resolvConf": "/etc/resolv.conf",
  "cpuCFSQuota": true,
  "cpuCFSQuotaPeriod": "100ms",
  "nodeStatusMaxImages": 50,
  "maxOpenFiles": 1000000,
  "contentType": "application/vnd.kubernetes.protobuf",
  "kubeAPIQPS": 50,
  "kubeAPIBurst": 100,
  "serializeImagePulls": false,
  "evictionHard": {
    "imagefs.available": "15%",
    "memory.available": "100Mi",
    "nodefs.available": "10%",
    "nodefs.inodesFree": "5%"
  },
  "evictionPressureTransitionPeriod": "5m0s",
  "enableControllerAttachDetach": true,
  "makeIPTablesUtilChains": true,
  "iptablesMasqueradeBit": 14,
  "iptablesDropBit": 15,
  "featureGates": {
    "APIPriorityAndFairness": true,
    "CSIMigrationAWS": false,
    "CSIMigrationAzureFile": false,
    "CSIMigrationGCE": false,
    "CSIMigrationvSphere": false,
    "DownwardAPIHugePages": true,
    "PodSecurity": true,
    "RotateKubeletServerCertificate": true
  },
  "failSwapOn": true,
  "memorySwap": {},
  "containerLogMaxSize": "50Mi",
  "containerLogMaxFiles": 5,
  "configMapAndSecretChangeDetectionStrategy": "Watch",
  "systemReserved": {
    "cpu": "500m",
    "memory": "1Gi"
  },
  "enforceNodeAllocatable": [
    "pods"
  ],
  "volumePluginDir": "/etc/kubernetes/kubelet-plugins/volume/exec",
  "logging": {
    "format": "text",
    "flushFrequency": 5000000000,
    "verbosity": 2,
    "options": {
      "json": {
        "infoBufferSize": "0"
      }
    }
  },
  "enableSystemLogHandler": true,
  "shutdownGracePeriod": "0s",
  "shutdownGracePeriodCriticalPods": "0s",
  "enableProfilingHandler": true,
  "enableDebugFlagsHandler": true,
  "seccompDefault": false,
  "registerNode": true,
  "kind": "KubeletConfiguration",
  "apiVersion": "kubelet.config.k8s.io/v1beta1"
}
```

When we get `KubeletConfig` for each node in all the pool, we will
compare them, and only save the consistent result to a file at 
`/api/v1/nodes/proxy/configz/{{pool_name}}`.

In the CaC content, we will deprecate the old kubelet rules, and add 
new ones to check `/api/v1/nodes/proxy/configz/{{pool_name}}`.

We will have a default variable `var_kubelet_pool` as `[worker, master]`.
and subsequently `/api/v1/nodes/proxy/configz/worker` and 
`/api/v1/nodes/proxy/configz/master` will be check for `KubeletConfig`
default configuration.


### User Stories

* As an Site Reliability Engineer managing multiple clusters with many nodes, I'd like to run
  compliance scans on my fleet in such a way that we have less compliance rule failures when
  no reboot remediation has been applied.

### API Extensions

None is changed


### Risks and Mitigations

There are no risks or mitigations for this change.

## Design Details

### Open Questions [optional]


1. Is `/api/v1/nodes/{NODE_NAME}/proxy/configz` a good place to check
   default `KubeletConfig`?

   Yes, it is a good place to check default `KubeletConfig`

2. If we want to check the KubeletConfig through Kubernetes API, the rule
   needs to change from node-rule to platform rule. Since platform rules
   does not care about node type, it will require all nodes to have the 
   same `KubeletConfig` configuration, wondering if that is a good idea?

   Only common `KubeletConfig` configuration will be saved to 
   `/api/v1/nodes/proxy/configz/{{pool_name}}`

### Test Plan

It will be tested and reflected in our pre-existing CI, some `KubeletConfig` 
checks will pass when the default `KubeletConfig` configuration is checked
by Compliance Operator and meets the compliance requirements.


### Upgrade / Downgrade Strategy


Upgrade expectations:
- No impact on existing production clusters

Downgrade expectations:
- Compliance Operator, as it is today, does not provide Downgrade options. This
  is not expected to change.

### Version Skew Strategy (TODO)

How will the component handle version skew with other components?
What are the guarantees? Make sure this is in the test plan.

Consider the following in developing a version skew strategy for this
enhancement:
- During an upgrade, we will always have skew among components, how will this impact your work?
- Does this enhancement involve coordinating behavior in the control plane and
  in the kubelet? How does an n-2 kubelet without this feature available behave
  when this feature is used?
- Will any other components on the node change? For example, changes to CSI, CRI
  or CNI may require updating that component before the kubelet.

### Operational Aspects of API Extensions

No API changes are introduced.

#### Failure Modes

- If the default `KubeletConfig` is not consistent across all nodes, some
  `KubeletConfig` check will fail.

  ex. If `"makeIPTablesUtilChains": true` configuration is not consistent across all nodes,
  we will leave out this configuration from the aggregated `KubeletConfig` result and
  issues an warning.

  When a compliance scan is being run, rules that checks for `makeIPTablesUtilChains`
  will fail, because the OpenSCAP won't find `makeIPTablesUtilChains` in `/api/v1/nodes/proxy/configz`

#### Support Procedures

There is no support procedure for this change, no action is required from the
user to use this enhancement.

## Implementation History

Major milestones in the life cycle of a proposal should be tracked in `Implementation
History`.

## Drawbacks

The idea is to find the best form of an argument why this enhancement should _not_ be implemented.

## Alternatives

Similar to the `Drawbacks` section the `Alternatives` section is used to
highlight and record other possible approaches to delivering the value proposed
by an enhancement.

## Infrastructure Needed [optional]

None