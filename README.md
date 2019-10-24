# compliance-operator

The compliance-operator is a OpenShift/Kubernetes Operator that runs an
openscap container in a pod on nodes in the cluster.

This repo is a POC for host openshift compliance detection and remediation
and is work-in-progress.

### Deploying:
```
$ (clone repo)
$ oc create -f deploy/ns.yaml
$ oc create -f deploy/crds/complianceoperator_v1alpha1_compliancescan_crd.yaml
$ oc create -f deploy/
$ vim deploy/crds/complianceoperator_v1alpha1_compliancescan_cr.yaml
# edit the file to your liking
$ oc create -f deploy/crds/complianceoperator_v1alpha1_compliancescan_cr.yaml
```

### Running the operator:
If you followed the steps above, the file called `deplou/operator.yaml`
also creates a deployment that runs the operator. If you want to run
the operator from the command line instead, delete the deployment and then
run:

```
OPERATOR_NAME=compliance-scan operator-sdk up local --namespace "compliance"
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
NAME                                                      DATA   AGE
example-compliancescan-ip-10-0-133-38.ec2.internal-pod    1      78s
example-compliancescan-ip-10-0-143-212.ec2.internal-pod   1      80s
example-compliancescan-ip-10-0-144-96.ec2.internal-pod    1      81s
example-compliancescan-ip-10-0-153-95.ec2.internal-pod    1      78s
example-compliancescan-ip-10-0-171-129.ec2.internal-pod   1      81s
example-compliancescan-ip-10-0-175-130.ec2.internal-pod   1      80s
```

A more convenient way to fetch the results is using
[a script](https://github.com/jhrozek/scapresults-k8s/blob/master/scapresults/fetchresults.py)
To use the script, clone the [scapresults-k8s repo](jhrozek/scapresults-k8s),
then run the `scapresults/fetchresults.py` script:
```
$ python3 scapresults/fetchresults.py --owner=example-openscap --namespace=compliance --dir=/tmp/results
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

### Overriding container images
Should you wish to override any of the two container images in the pod, you can
do so using environment variables:
    * `OPENSCAP_IMAGE` for the scanner container
    * `LOG_COLLECTOR_IMAGE` for the log collecting container

For example, to run the log collector from a different branch:
```
make run LOG_COLLECTOR_IMAGE=quay.io/jhrozek/scapresults-k8s:testbranch
```

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
