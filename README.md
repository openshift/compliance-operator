# compliance-operator

The compliance-operator is a OpenShift/Kubernetes Operator that runs an
openscap container in a pod on nodes in the cluster.

This repo is a POC for host openshift compliance detection and remediation
and is work-in-progress.

### Deploying:
First, become kubeadmin, either with `oc login` or by exporting `KUBECONFIG`.
```
$ (clone repo)
$ oc create -f deploy/ns.yaml
$ for f in $(ls -1 deploy/crds/*crd.yaml); do oc create -f $f; done
$ oc create -f deploy/
$ vim deploy/crds/complianceoperator.compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml
# edit the file to your liking
$ oc create -f deploy/crds/complianceoperator.compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml
```

### Running the operator:
If you followed the steps above, the file called `deploy/operator.yaml`
also creates a deployment that runs the operator. If you want to run
the operator from the command line instead, delete the deployment and then
run:

```
make run
```

At this point the operator would pick up the CRD, create a scan for the
suite, the scan would create a pod for every node in the cluster (this will
change, see below), execute `openscap-scan` on every node and eventually
report the results. The scan using one rule takes about a minute.

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

In case there's multiple scans running, it might be handy to distinguish
between scan results coming from different scans. You can take advantage
of the configmaps being labeled with the scan name to do that:
```
$ oc get cm --show-labels
NAME                                                      DATA   AGE     LABELS
example-compliancescan-ip-10-0-131-249.ec2.internal-pod   1      2m16s   compliance-scan=example-compliancescan
example-compliancescan-ip-10-0-132-37.ec2.internal-pod    1      2m16s   compliance-scan=example-compliancescan
example-compliancescan-ip-10-0-144-8.ec2.internal-pod     1      2m10s   compliance-scan=example-compliancescan
example-compliancescan-ip-10-0-149-53.ec2.internal-pod    1      2m20s   compliance-scan=example-compliancescan
example-compliancescan-ip-10-0-164-150.ec2.internal-pod   1      2m9s    compliance-scan=example-compliancescan
example-compliancescan-ip-10-0-174-131.ec2.internal-pod   1      2m16s   compliance-scan=example-compliancescan
```

At the moment, the scans produce XML-based ARF reports, which the operator
is able to parse. You can fetch the results with `oc extract $cm_name`.

A more convenient way to fetch the results is using
[a script](https://github.com/jhrozek/scapresults-k8s/blob/master/scapresults/fetchresults.py)
To use the script, clone the [scapresults-k8s repo](jhrozek/scapresults-k8s),
then run the `scapresults/fetchresults.py` script:
```
$ python3 scapresults/fetchresults.py --owner=example-openscap --namespace=openshift-compliance --dir=/tmp/results
```
The parameters you need to supply is the name of the scan CRD through the
`--owner` CLI flag and the namespace. The output directory is optional and
defaults to the current working directory.

The pods and the configMaps are not garbage-collected automatically, but are owned by the CRD,
so removing the CRD removes the dependent artifacts.

#### Scan only a subset of nodes
It might be handy to run different compliance checks
on some nodes, but not others. For example, some nodes might run a different
OS than others or a particular check might make sense for master
nodes only.

To constrain a scan to a subset of nodes, define a `nodeSelector`
in the CR. For example, to run a scan on master nodes only, add this:
```
nodeSelector:
    node-role.kubernetes.io/master: ""
```

To see the available labels, run `oc get nodes --show-labels` or
`oc describe node/$nodename`.

### Related repositories
The pods that the operator consist of two containers. One is the openscap
container itself at [https://github.com/jhrozek/openscap-ocp](https://github.com/jhrozek/openscap-ocp)
and the other is a log-collector at [https://github.com/openshift/scapresults](https://github.com/openshift/scapresults)

### Overriding container images
Should you wish to override any of the two container images in the pod, you can
do so using environment variables:
    * `OPENSCAP_IMAGE` for the scanner container
    * `LOG_COLLECTOR_IMAGE` for the log collecting container

For example, to run the log collector from a different branch:
```
make run OPENSCAP_IMAGE=quay.io/jhrozek/openscap-ocp:rhel8-2-test
```
