# openscap-operator

The openscap-operator is a OpenShift/Kubernetes Operator that runs an
openscap container in a pod on nodes in the cluster.

This repo is a POC for host openshift compliance detection and remediation
and is work-in-progress.

### Deploying:
```
$ (clone repo)
$ oc create -f deploy/ns.yaml
$ oc create -f deploy/role.yaml
$ oc create -f deploy/service_account.yaml
$ oc create -f deploy/role_binding.yaml
$ oc create -f deploy/crds/openscap_v1alpha1_openscap_crd.yaml
$ vim deploy/crds/openscap_v1alpha1_openscap_cr.yaml
# edit the file to your liking
$ oc create -f deploy/crds/openscap_v1alpha1_openscap_cr.yaml
```

### Running the operator:
At the moment, I only tried running the operator out of the cluster.

```
OPERATOR_NAME=openscap-scan operator-sdk up local --namespace "openscap"
```

At this point the operator would pick up the CRD, create a pod for every
node in the cluster (this will change, see below), execute `openscap-scan`
on every node and eventually report the results. The scan using one rule
takes about a minute.

You can watch the node progress with:
```
$ oc get pods -w
```

When the scan is done, the operator would change the state of the OpenScap
object to "Done" and all the pods would be "Completed". You can then view
the results in configmaps, e.g.:
```
$ oc get cm
NAME                                                DATA   AGE
example-openscap-ip-10-0-133-236.ec2.internal-pod   1      7m17s
example-openscap-ip-10-0-134-19.ec2.internal-pod    1      7m19s
example-openscap-ip-10-0-152-226.ec2.internal-pod   1      7m20s
example-openscap-ip-10-0-156-38.ec2.internal-pod    1      7m19s
example-openscap-ip-10-0-162-167.ec2.internal-pod   1      7m20s
example-openscap-ip-10-0-166-21.ec2.internal-pod    1      7m19s
$ oc describe cm/example-openscap-ip-10-0-133-236.ec2.internal-pod
```

A more convenient way to fetch the results is using
[a script](https://github.com/jhrozek/scapresults-k8s/blob/master/scapresults/fetchresults.py)
To use the script, clone the [scapresults-k8s repo](jhrozek/scapresults-k8s),
then run the `scapresults/fetchresults.py` script:
```
$ python3 scapresults/fetchresults.py --owner=example-openscap --namespace=openscap --dir=/tmp/results
```
The parameters you need to supply is the name of the scan CRD through the
`--owner` CLI flag and the namespace. The output directory is optional and
defaults to the current working directory.

The pods and the configMaps are not garbage-collected automatically, but are owned by the CRD,
so removing the CRD removes the dependent artifacts.

### Related repositories
The pods that the operator consist of two containers. One is the openscap
container itself at [https://github.com/jhrozek/openscap-ocp](jhrozek/openscap-ocp)
and the other is a log-collector at [https://github.com/jhrozek/scapresults-k8s](jhrozek/scapresults-k8s)


## TODO
- using a configMap for reporting is not very nice using a volume would be nicer
  - but using a volume across nodes seems to be tricky, maybe we could at least
  collect the configMap contents to the volume?
- packaging
- review the container/pod permissions
- use a NodeSelector to select the nodes to scan
- should the operator be cluster-wise and nor require its own namespace?
- container todo:
  - Use UBI as the base image, not Fedora
