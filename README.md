# compliance-operator

The compliance-operator is a OpenShift Operator that allows an administrator
to run compliance scans and provide remediations for the issues found. The
operator leverages OpenSCAP under the hood to perform the scans.

The operator runs in the `openshift-compliance` namespace, so make sure
all namespaced resources like the deployment or the custom resources the
operator consumes are created there.

## Deploying the operator
Before you can actually use the operator, you need to make sure it is
deployed in the cluster.

### Deploying from source
First, become kubeadmin, either with `oc login` or by exporting `KUBECONFIG`.
```
$ (clone repo)
$ oc create -f deploy/ns.yaml
$ for f in $(ls -1 deploy/crds/*crd.yaml); do oc create -f $f; done
$ oc create -f deploy/
```

### Running the operator locally
If you followed the steps above, the file called `deploy/operator.yaml`
also creates a deployment that runs the operator. If you want to run
the operator from the command line instead, delete the deployment and then
run:

```
make run
```
This is mostly useful for local development.

## Running a scan

The API resource that you'll be interacting with is called `ComplianceSuite`.
It is a collection of `ComplianceScan` objects, each of which describes
a scan. In addition, the `ComplianceSuite` also contains an overview of the
remediations found and the statuses of the scans. Typically, you'll want
to map a scan to a `MachinePool`, mostly because the remediations for any
issues that would be found contain `MachineConfig` objects that must be
applied to a pool.

To run the scans, copy and edit the example file at
`deploy/crds/complianceoperator.compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml`
and create the Kubernetes object:
```
# edit the Suite definition to your liking. You can also copy the file and edit the copy.
$ vim deploy/crds/complianceoperator.compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml
# make sure to switch to the openshift-compliance namespace
$ oc project openshift-compliance
$ oc create -f deploy/crds/complianceoperator.compliance.openshift.io_v1alpha1_compliancesuite_cr.yaml
```

At this point the operator reconciles the `ComplianceSuite` custom resource,
and creates the `ComplianceScan` objects for the suite. The `ComplianceScan`
then creates scan pods that run on each node in the cluster. The scan
pods execute `openscap-chroot` on every node and eventually report the
results. The scan takes several minutes to complete.

You can watch the scan progress with:
```
$ oc get compliancesuites/$suite-name -oyaml
```
and even the individual pods with:
```
$ oc get pods -w
```

When the scan is done, the operator changes the state of the ComplianceSuite
object to "Done" and all the pods are transition to the "Completed"
state. You can then check the `ComplianceRemediations` that were found with:
```
$ oc get complianceremediation
NAME                                       AGE
workers-scan-chronyd-client-only           15m
workers-scan-chronyd-no-chronyc-network    15m
workers-scan-coredump-disable-backtraces   15m
workers-scan-coredump-disable-storage      15m
workers-scan-no-direct-root-logins         15m
workers-scan-no-empty-passwords            15m
```

To apply a remediation, edit that object and set its `Apply` attribute
to `true`:
```
$ oc edit complianceremediation/workers-scan-no-direct-root-logins
```

The operator then aggregates all applied remediations and create a
`MachineConfig` object per scan. This `MachineConfig` object is rendered
to a `MachinePool` and the `MachineConfigDeamon` running on nodes in that
pool pushes the configuration to the nodes and reboots the nodes.

You can watch the node status with:
```
$ oc get nodes
```

Once the nodes reboot, you might want to run another Suite to ensure that
the remediation that you applied previously was no longer found.

## Extracting results

The scans provide two kinds of results: the full report in the ARF format
and just the list of scan results in the XCCDF format. The ARF reports are,
due to their large size, copied into persistent volumes:
```
oc get pv
NAME                                       CAPACITY  CLAIM
pvc-5d49c852-03a6-4bcd-838b-c7225307c4bb   1Gi       openshift-compliance/workers-scan
pvc-ef68c834-bb6e-4644-926a-8b7a4a180999   1Gi       openshift-compliance/masters-scan
```

To view the results at the moment, you'd have to start a pod manually, mount
the PV into the pod and e.g. serve the results over HTTP. We're working on
a better solution in the meantime.

The XCCDF results are much smaller and can be stored in a configmap, from
which you can extract the results. For easier filtering, the configmaps
are labeled with the scan name:
```
$ oc get cm -l=compliance-scan=masters-scan
NAME                                            DATA   AGE
masters-scan-ip-10-0-129-248.ec2.internal-pod   1      25m
masters-scan-ip-10-0-144-54.ec2.internal-pod    1      24m
masters-scan-ip-10-0-174-253.ec2.internal-pod   1      25m
```

To extract the results, use:
```
$ oc extract cm/masters-scan-ip-10-0-174-253.ec2.internal-pod
```

### Overriding container images
Should you wish to override any of the two container images in the pod, you can
do so using environment variables:
    * `OPENSCAP_IMAGE` for the scanner container
    * `LOG_COLLECTOR_IMAGE` for the log collecting container
    * `RESULT_SERVER_IMAGE` for the container that collects ARF reports and puts them in a PV
    * `REMEDIATION_AGGREGATOR_IMAGE` for the container that collects XCCDF results and puts them in a ConfigMap

For example, to run the log collector from a different branch:
```
make run OPENSCAP_IMAGE=quay.io/jhrozek/openscap-ocp:rhel8-2-test
```
