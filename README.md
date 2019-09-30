# openscap-operator

The openscap-operator is a OpenShift/Kubernetes Operator that runs an
openscap container in a pod on nodes in the cluster.

This repo is a POC for host openshift compliance detection and remediation
and is work-in-progress.

### Deploying:
```
$ (clone repo)
$ oc create -f deploy/ns.yaml
$ oc create -f deploy/crds/openscap_v1alpha1_openscap_crd.yaml
$ vim deploy/crds/openscap_v1alpha1_openscap_cr.yaml
# edit the file to your liking
$ oc create -f deploy/crds/openscap_v1alpha1_openscap_cr.yaml
```

### Running the operator:
```
OPERATOR_NAME=openscap-scan operator-sdk up local --namespace "openscap"
```

At this point the operator would pick up the CRD, create a pod for every
node in the cluster (this will change, see below), execute `openscap-scan`
on every node and eventually report the results.

You can watch the node progress with:
```
$ oc get pods -w
```
At the moment, the scan result can only be viewed with:
```
$ oc logs $PODNAME
```

The pods are not garbage-collected automatically, but are owned by the CRD,
so removing the CRD removes the pods.

## TODO
- log reporting via a configmap or a volume
- packaging
- permissions
    - service account, RBAC
- use a NodeSelector to select the nodes to scan
- should the operator be cluster-wise and nor require its own namespace?
- container todo:
    - the host mount/chroot is hardcoded at the moment, this should be
      configured by the operator via env var
    - the container should probably use an entrypoint
