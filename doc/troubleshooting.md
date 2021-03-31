# Troubleshooting Compliance Operator
This document describes how to troubleshoot problems with the Compliance
Operator. The information can be useful either to diagnose a problem or
provide information in a bug report.

Please refer to the README for general information about the operator and
to a (CRD document)[crds.md] to learn more API objects.

## General tips

   * The Compliance Operator emits Kubernetes events when something
     important happens. You can either view all events in the cluster (`oc get events
     -nopenshift-compliance`) or events for an object, e.g. for a scan
     (`oc describe compliancescan/$scan`)

   * The Compliance Operator consists of several controllers, roughly
     one per API object. It could be handy to filter only those controller that correspond to
     the API object having issues, e.g. if a `ComplianceRemediation` can't be applied,
     the first place to look might be the messages from the `remediationctrl` controller.
     You can filter the messages from a single controller e.g. using `jq`:
     `oc logs compliance-operator-775d7bddbd-gj58f | jq -c 'select(.logger == "profilebundlectrl")' `

   * The timestamps are logged as seconds since UNIX epoch in UTC. To convert
     them to a human-readable date, use
     `date -d @timestamp --utc`, e.g. `date -d @1596184628.955853 --utc`

   * Many CRs, most importantly `ComplianceSuite` and `ScanSetting` allow
     the `debug` option to be set. Enabling this option increases verbosity
     of the openscap scanner pods as well as some other helper pods.

   * If a single rule is passing or failing unexpectedly, it could be helpful
     to run a single scan or a suite with only that rule - you can find the
     rule ID from the corresponding `ComplianceCheckResult` object and use it
     as the `rule` attribute value in a Scan CR. Then, together with the
     `debug` option enabled, the `scanner` container logs in the scanner
     pod would show the raw OpenSCAP logs.

## Anatomy of a scan

Debugging a problem is easier when the control flow of the operator is
clear.  Let's illustrate the complete flow with an example - we'll define
a Suite using the high-level `ScanSetting/ScanSettingBinding` objects,
run it, remediate a failing check and re-run the scan again to see the
difference. Along the way, we'll illustrate which object is reconciled
at that point and which controller is doing the work.

## Compliance sources
First, the compliance content must come from somewhere. We're
going to be referencing a `Profile` object that is generated from a
`ProfileBundle`. Compliance operator creates two (one for the cluster,
one for the cluster nodes) `ProfileBundles` by default:
```shell
oc get profilebundle.compliance
oc get profile.compliance
```
The `ProfileBundle` objects are processed by deployments labeled with the
`Bundle` name, so in order to debug an issue with the `Bundle,` you can find
the deployment and check out logs of the pods in that deployment, for
example for the `ocp4` profile bundle:
```shell
oc logs -lprofile-bundle=ocp4
oc get deployments,pods -lprofile-bundle=ocp4
oc logs pods/...
```

## The ScanSetting and ScanSettingBinding lifecycle and debugging
Provided we have valid compliance content sources, we can use the
high-level `ScanSetting` and `ScanSettingBinding` objects to generate
`ComplianceSuite` and `ComplianceScan` objects respectively:

```
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: my-companys-constraints
debug: true
# For each role, a separate scan will be created pointing
# to a node-role specified in roles
roles:
  - worker
---
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: my-companys-compliance-requirements
profiles:
  # Node checks
  - name: rhcos4-e8
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
  # Cluster checks
  - name: ocp4-e8
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
settingsRef:
  name: my-companys-constraints
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
```
Both `ScanSetting` and `ScanSettingBinding` objects are handled by the
same controller tagged with `logger=scansettingbindingctrl`.  These objects
have no status, any issues are communicated in form of events. On success,
you'd see something like:
```
Events:
  Type     Reason        Age    From                    Message
  ----     ------        ----   ----                    -------
  Normal   SuiteCreated  9m52s  scansettingbindingctrl  ComplianceSuite openshift-compliance/my-companys-compliance-requirements created
```
And a `ComplianceSuite` object is created. At that point, the flow continues
to reconcile the newly created `ComplianceSuite`.

## ComplianceSuite lifecycle and debugging
The `ComplianceSuite` CR is mostly a wrapper around `ComplianceScan` CRs. The
`ComplianceSuite` CR is handled by controller tagged with `logger=suitectrl`.
This controller handles creating Scans from a Suite, reconciling and
aggregating individual Scan statuses into a single Suite status. If a Suite
is set to execute periodically, the `suitectrl` also handles creating a
`CronJob` CR that re-runs the Scans in the Suite after the initial run
is done:
```shell
oc get cronjobs
NAME                                           SCHEDULE    SUSPEND   ACTIVE   LAST SCHEDULE   AGE
my-companys-compliance-requirements-rerunner   0 1 * * *   False     0        <none>          151m
```

For the most important issues, Events are emitted, view them with `oc
describe compliancesuites/$name`. The Suite objects also have a Status
subresource that is updated when any of Scan objects that belong to
this suite update their Status subresource (unless an error happens
when processing the Suite itself before the Scans are created)

As long as all expected scans are created, the control is handed over to
the scan controller.

## ComplianceScan lifecycle and debugging
The `ComplianceScan` CRs are handled by the `scanctrl` controller. This
is also where the actual scans happen and the scan results are created.
Each scan goes through several phases:

### Pending phase
The scan is just validated for correctness in this phase. If some parameters
like storage size are invalid, the scan transitions to DONE with ERROR result,
otherwise proceeds to the Launching phase.

### Launching phase
In this phase, several `ConfigMaps` that contain either environment for the
scanner pods or directly the script that the scanner pods will be evaluating.
List the CMs with:
```shell
oc get cm -lcompliance.openshift.io/scan-name=$scan_name,complianceoperator.openshift.io/scan-script=
```
e.g.:
```shell
oc get cm -lcompliance.openshift.io/scan-name=rhcos4-e8-worker,complianceoperator.openshift.io/scan-script=
```
These `ConfigMaps` will be used by the scanner pods. If you ever needed to
modify the scanner behaviour, change the scanner debug level or print the
raw results, modifying the `ConfigMaps` is the way to go.

Afterwards, a `PersistentVolumeClaim` is created per scan in order to store the
raw ARF results:
```
oc get pvc -l$scan_name
```
Refer to the README for information on how to mount and read the ARF results.

The PVCs are mounted by a per-scan `ResultServer` deployment. A
`ResultServer` is a simple HTTP server where the individual scanner pods,
each of which might be running on a different node, upload the full
ARF results to. This might seem odd, but the full ARF results might be
quite big and at the same time, without knowing details about the storage
configured on the cluster, we can't presume that it would be possible
to create a volume that could be mounted from multiple nodes at the same
time. After the scan is finished, the `ResultServer` deployment is scaled
down and the PVC with the raw results can be mounted from another custom
pod and the results can be fetched or inspected. The traffic between the
scanner pods and the `ResultServer` is protected by mutual TLS.

Finally, the scanner pods are launched in this phase; one scanner pod for
a `Platform` scan instance and one scanner pod per matching node for a
`Node` scan instance. The per-node pods are labeled with the node name,
each pod is always labeled with the `ComplianceScan` name, for example:
```
oc get pods -lcompliance.openshift.io/scan-name=rhcos4-e8-worker,workload=scanner --show-labels
NAME                                                              READY   STATUS      RESTARTS   AGE   LABELS
rhcos4-e8-worker-ip-10-0-169-90.eu-north-1.compute.internal-pod   0/2     Completed   0          39m   compliance.openshift.io/scan-name=rhcos4-e8-worker,targetNode=ip-10-0-169-90.eu-north-1.compute.internal,workload=scanner
```

At this point, the scan proceeds to the Running phase.

### Running phase
The running phase simply waits until the scanner pods finish. In case you
need to debug the scanner pods, it might be useful to learn what containers
are running there -- when the scan itself is having issues, either the
scanner pods or the artifacts it creates such as the resulting ConfigMaps
might be a good place to start debugging:

   * **init container**: There is one init container called
     `content-container.` It runs the *contentImage* container and
     executes a single command that copies the *contentFile* to the `/content`
     directory shared with the other containers in this pod
   * **scanner**: This container runs the actual scan. For Node scans, the container
     mounts the node filesystem as `/host` and mounts the content delivered by the
     init container. The container also mounts the `entrypoint`
     `ConfigMap` created in the Launching phase and executes it. The default
     script in the entrypoint `ConfigMap` executes openscap and stores the result
     files in the `/results` directory shared between the pod's containers.
     Logs from this pod, especially when the `debug` flag is enabled, allow
     you to see what exactly did the openscap scanner check for.
   * **logcollector**: The logcollector container waits until the scanner
      container finishes. Then, it uploads the full ARF results to the
      `ResultServer` and separately uploads the XCCDF results along with scan
      result and openscap result code as a `ConfigMap.` These result configmaps
      are labeled with the scan name (`compliance.openshift.io/scan-name=$scan_name`):
      ```
      $ oc describe cm/rhcos4-e8-worker-ip-10-0-169-90.eu-north-1.compute.internal-pod
      Name:         rhcos4-e8-worker-ip-10-0-169-90.eu-north-1.compute.internal-pod
      Namespace:    openshift-compliance
      Labels:       compliance.openshift.io/scan-name-scan=rhcos4-e8-worker
                    complianceoperator.openshift.io/scan-result=
      Annotations:  compliance-remediations/processed: 
                    compliance.openshift.io/scan-error-msg: 
                    compliance.openshift.io/scan-result: NON-COMPLIANT
                    openscap-scan-result/node: ip-10-0-169-90.eu-north-1.compute.internal

      Data
      ====
      exit-code:
      ----
      2
      results:
      ----
      <?xml version="1.0" encoding="UTF-8"?>
      ...

      ```

Scanner pods for `Platform` scans are similar, except:
    * There is one extra init container called `api-resource-collector` that
      reads the OpenScap content provided by the content-container init,
      container, figures out which API resources the content needs to
      examine and stores those API resources to a shared directory where the
      `scanner` container would read them from.
    * The `scanner` container does not need to mount the host filesystem

When the scanner pods are done, the scans move on to the Aggregating phase.

### Aggregating phase
In the aggregating phase, the scan controller spawns yet another pod called
the aggregator pod. Its purpose it to take the result `ConfigMap` objects,
read the results and for each check result create the corresponding k8s
object and, if the check failure can be automatically remediated, also creates
a `ComplianceRemediation` object. In order to provide human-readable metadata
for the checks and remediations, the aggregator pod also mounts the OpenScap
content using an init container.

When a `ConfigMap` is processed by an aggregator pod,it is labeled the
`compliance-remediations/processed` label.

The result of this phase are `ComplianceCheckResult` objects:
```
oc get compliancecheckresults -lcompliance.openshift.io/scan-name=rhcos4-e8-worker
NAME                                                       STATUS   SEVERITY
rhcos4-e8-worker-accounts-no-uid-except-zero               PASS     high
rhcos4-e8-worker-audit-rules-dac-modification-chmod        FAIL     medium
```
and `ComplianceRemediation` objects:
```
oc get complianceremediations -lcompliance.openshift.io/scan-name=rhcos4-e8-worker
NAME                                                       STATE
rhcos4-e8-worker-audit-rules-dac-modification-chmod        NotApplied
rhcos4-e8-worker-audit-rules-dac-modification-chown        NotApplied
rhcos4-e8-worker-audit-rules-execution-chcon               NotApplied
rhcos4-e8-worker-audit-rules-execution-restorecon          NotApplied
rhcos4-e8-worker-audit-rules-execution-semanage            NotApplied
rhcos4-e8-worker-audit-rules-execution-setfiles            NotApplied
```

Once these CRs are created, the aggregator pod exits and the scan
moves on to the Done phase.

### Done phase
In the final scan phase, the scan resources are cleaned up if needed and the
`ResultServer` deployment is either scaled down (if the scan was one-time)
or deleted if the scan is continuous; the next scan instance would then
recreate the deployment again.

It is also possible to trigger a re-run of a scan in the Done phase by
annotating it:
```
oc annotate compliancescans/rhcos4-e8-worker compliance.openshift.io/rescan=
```

After the scan reaches the Done phase, nothing else happens on its
own unless the remediations are set to be applied automatically with
`autoApplyRemediations=true`. Typically, the administrator would now review
the remediations and apply them as needed.

If the remediations are set to be applied automatically, the `ComplianceSuite`
controller takes over in the Done phase, pauses the `MachineConfigPool` to
which the scan maps to and applies all the remediations in one go.

Either way, if a remediation is applied, the `ComplianceRemediation`
controller takes over.

## ComplianceRemediation lifecycle and debugging
At this point, our example scan has reported some findings. We can enable
one of the remediations by toggling its `apply` attribute to `true`:
```
oc patch complianceremediations/rhcos4-e8-worker-audit-rules-dac-modification-chmod --patch '{"spec":{"apply":true}}' --type=merge
```

The complianceremediation controller (`logger=remediationctrl`) reconciles
the modified object. The result of the reconciliation is change of status of
the remediation object that is reconciled, but also the actual remediation
object is created. For remediations that come from Node-type scans, the
remediations are `MachineConfig` objects, one per remediation, for Platform-type
scans, the remediadions can be any generic Kubernetes object (e.g. a ConfigMap).

The `MachineConfig` objects always begin with `75-` and are named after the
`ComplianceRemediation` they come from, in our case:
```
oc get mc | grep 75-
75-rhcos4-e8-master-audit-rules-dac-modification-chmod
75-rhcos4-e8-master-audit-rules-dac-modification-chown
75-rhcos4-e8-master-audit-rules-dac-modification-fchmod
75-rhcos4-e8-master-audit-rules-dac-modification-fchmodat
75-rhcos4-e8-master-audit-rules-dac-modification-fchown
```

The remediation loop ends once the rendered MachineConfig is updated, if needed,
and the reconciled remediation object status is updated.

In our case, applying a remediation would trigger a reboot. After that we can
annotate the scan to re-run it:
```
oc annotate compliancescans/rhcos4-e8-worker compliance.openshift.io/rescan=
```
Afterwards, we can wait for the scan run to finish at which point the check for
the remediation we applied should pass:
```
oc get compliancecheckresults/rhcos4-e8-worker-audit-rules-dac-modification-chmod
NAME                                                  STATUS   SEVERITY
rhcos4-e8-worker-audit-rules-dac-modification-chmod   PASS     medium
```

## Useful labels

Each pod that's spawned by the compliance-operator is labeled specifically with
the scan it belongs to and the work it does.

The scan identifier is labeled with the `compliance.openshift.io/scan-name`
label.

The workload identifier is labeled with the `workload` label.

The compliance-operator schedules the following workloads:

* **scanner**: Performs the actual compliance scan.

* **resultserver**: Stores the raw results for the compliance scan.

* **aggregator**: Aggregates the results, detects inconsistencies and outputs
  result objects (checkresults and remediations).

* **suitererunner**: Will tag a suite to be re-run (when a schedule is set).

* **profileparser**: Parses a datastream and creates the appropriate profiles,
  rules and variables.

So, when debugging and needing the logs for a certain workload, it's possible
to do:

`oc logs -l workload=<workload name>`

## Useful annotations

### Re-scan a ComplianceScan

To force a scan to run again you can use the following annotation:

```
compliance.openshift.io/rescan
```

One may set it with the `oc` command as follows:

```
oc annotate compliancescans/$SCAN_NAME compliance.openshift.io/rescan=
```

### Apply remediations generated by suite's scans

While it's possible to use the `autoApplyRemediations` boolean parameter from a
ComplianceSuite object, it's also possible to annotate the object in order for
the Operator to apply all the remediations that were created. To do so, one may
use the following annotation:

``` 
compliance.openshift.io/apply-remediations
```

One may use it as follows

```
oc annotate compliancesuites/$SUITE_NAME compliance.openshift.io/apply-remediations=
```

### Auto update remediations

There are cases where a scan with newer content may mark remediations as
`OUTDATED`, in this case, if the administrator wants to apply the remediations
and remove the outdated ones (which will apply the newer ones), the following
annotation may be used:

``` 
compliance.openshift.io/remove-outdated
```

One may use it as follows

```
oc annotate compliancesuites/$SUITE_NAME compliance.openshift.io/remove-outdated=
```

Alternatively, you can set the `autoUpdateRemediations` flag in a `ScanSetting`
or a `ComplianceSuite` object to update the remediations automatically.
