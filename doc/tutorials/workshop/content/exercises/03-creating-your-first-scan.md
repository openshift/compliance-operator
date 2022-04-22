---
Title: Scanning
PrevPage: 02-installation
NextPage: 04-remediations
---

This chapter will show you how to create, run and evaluate a compliance
scan. Before actually starting a scan, we need to select a baseline we'll
be scanning against.

First, make sure that you're in the appropriate namespace for working with the
operator. To do so, do:

```
oc project openshift-compliance
```

If you're familiar with OpenSCAP, you might be already familiar with how the
compliance content is shipped, with the concept of profiles, distributed
in XML files known as Data Streams. If not, it's not really required knowledge.

For consumption by the Compliance Operator, the compliance content is
packaged in container images, subsequently the Compliance Operator wraps
and exposes the compliance content in a CustomResource for better
usability:

  * **rule.compliance** is a single compliance check. For example the rule
    `rhcos4-service-auditd-enabled` checks if the `auditd` daemon is running on
    RHCOS.
  * **profile.compliance** is a collection of rules that form a single
    compliance baseline for a product. For example the `rhcos4-e8` profile
    implements the Australian Government's "Essential Eight" standard for
    RHCOS, `ocp4-e8` implements the same standard for OCP.
  * **profilebundle.compliance** is a collection of profiles for a single
    product where product might be `ocp4` or `rhcos4`.

By default, the Compliance Operator creates two `profilebundle` objects, one for
OCP and one for RHCOS based on the [upstream ComplianceAsCode content images](https://quay.io/repository/compliance-operator/compliance-operator-content):
```
$ oc get profilebundle.compliance
NAME     CONTENTIMAGE                           CONTENTFILE         STATUS
ocp4     quay.io/compliance-operator/compliance-operator-content:latest   ssg-ocp4-ds.xml     VALID
rhcos4   quay.io/compliance-operator/compliance-operator-content:latest   ssg-rhcos4-ds.xml   VALID
```

Inspecting the ProfileBundle objects, you'll see that they mostly point to the
content image and a file inside the image, relative to the root directory:
```
$ oc get profilebundle.compliance rhcos4 -o yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ProfileBundle
metadata:
  name: rhcos4
  namespace: openshift-compliance
  selfLink: /apis/compliance.openshift.io/v1alpha1/namespaces/openshift-compliance/profilebundles/rhcos4
  uid: f5516313-5f16-4ff8-9c69-d79d44126b8b
spec:
  contentFile: ssg-rhcos4-ds.xml
  contentImage: quay.io/compliance-operator/compliance-operator-content:latest
status:
  dataStreamStatus: VALID
```
The `status.dataStreamStatus` field is set by the operator and reflects
the result of the content parsing.

Several `Profile` objects are parsed out of each bundle, for the `rhcos4` bundle we'd have:
```
$ oc get profile.compliance -lcompliance.openshift.io/profile-bundle=rhcos4  -nopenshift-compliance
NAME              AGE
rhcos4-e8         5h2m
rhcos4-moderate   5h2m
rhcos4-ncp        5h2m
rhcos4-ospp       5h2m
rhcos4-stig       5h2m
```

For the rest of the chapter we'll be working with the `e8` profile. Inspecting
the profile shows the following:
```
$ oc get profile.compliance rhcos4-e8 -o yaml
apiVersion: compliance.openshift.io/v1alpha1
description: |-
  This profile contains configuration checks for Red Hat Enterprise Linux CoreOS
  that align to the Australian Cyber Security Centre (ACSC) Essential Eight.

  A copy of the Essential Eight in Linux Environments guide can be found at the
  ACSC website:

  https://www.cyber.gov.au/publications/essential-eight-in-linux-environments
id: xccdf_org.ssgproject.content_profile_e8
kind: Profile
metadata:
  annotations:
    compliance.openshift.io/image-digest: pb-rhcos496gpm
    compliance.openshift.io/product: redhat_enterprise_linux_coreos_4
    compliance.openshift.io/product-type: Node
  creationTimestamp: "2020-09-08T07:44:10Z"
  generation: 1
  labels:
    compliance.openshift.io/profile-bundle: rhcos4
  name: rhcos4-e8
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ProfileBundle
    name: rhcos4
    uid: a130fef5-054c-431e-91c7-306995ee86c4
  resourceVersion: "39697"
  selfLink: /apis/compliance.openshift.io/v1alpha1/namespaces/openshift-compliance/profiles/rhcos4-e8
  uid: d2c28fb8-0bfe-4c7e-884e-d5e1be790d3e
rules:
- rhcos4-accounts-no-uid-except-zero
- rhcos4-audit-rules-dac-modification-chmod
- rhcos4-audit-rules-dac-modification-chown
- rhcos4-audit-rules-execution-chcon
- rhcos4-audit-rules-execution-restorecon
- rhcos4-audit-rules-execution-semanage
- rhcos4-audit-rules-execution-setfiles
- rhcos4-audit-rules-execution-setsebool
- rhcos4-audit-rules-execution-seunshare
- rhcos4-audit-rules-kernel-module-loading
- rhcos4-audit-rules-login-events
- rhcos4-audit-rules-login-events-faillock
- rhcos4-audit-rules-login-events-lastlog
- rhcos4-audit-rules-login-events-tallylog
...
...
title: Australian Cyber Security Centre (ACSC) Essential Eight
```

An important thing to note is that the object provides human-readable title
descriptions. In `metadata.annotations` we see `product-type` and `product` -
the former is set to `Node` in this profile, indicating that it only applies to
scans of the cluster node, being an assessment of the compliance state of the
nodes. The only other allowed `product-type` value you would see with the
`ocp4-*` profiles is `Platform` which denotes that the profile only applies to
scans of Kubernetes API resources, being an assessment of the compliance state
of the platform.

By organizing the profiles into these two types, it allows proper privilege
separation at the operator level. The Platform checks only require the
`cluster-reader` role, whereas the Node checks require a privileged host mount,
so the operator uses independent workloads for each type, applying the
principle of least privilege. The `product` annotation identifies the node or
platform that this profile targets, in this case it's RHCOS 4.

Finally and most importantly, the profile includes a large number of compliance
rules that actually comprise the profile. We're going to explore the
rules next. To view a rule, call:
```
$ oc get rule.compliance rhcos4-accounts-no-uid-except-zero -nopenshift-compliance -oyaml
```
The (trimmed) result looks somewhat like:
```
$ oc get rule.compliance rhcos4-accounts-no-uid-except-zero -o yaml
apiVersion: compliance.openshift.io/v1alpha1
description: <br />If the account is associated with system commands or applications the UID&#xA;should be changed to one greater than &#34;0&#34; but less than &#34;1000.&#34;&#xA;Otherwise assign a UID greater than &#34;1000&#34; that has not already been&#xA;assigned.
id: xccdf_org.ssgproject.content_rule_accounts_no_uid_except_zero
kind: Rule
metadata:
  annotations:
    compliance.openshift.io/image-digest: pb-rhcos4mxp5c
    compliance.openshift.io/rule: accounts-no-uid-except-zero
    control.compliance.openshift.io/NIST-800-53: IA-2;AC-6(5);IA-4(b)
    policies.open-cluster-management.io/controls: IA-2,AC-6(5),IA-4(b)
    policies.open-cluster-management.io/standards: NIST-800-53
  creationTimestamp: "2020-09-09T07:34:16Z"
  generation: 1
  labels:
    compliance.openshift.io/profile-bundle: rhcos4
  name: rhcos4-accounts-no-uid-except-zero
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ProfileBundle
    name: rhcos4
    uid: 597792a5-1caa-4857-8611-ac2301b7f4c2
  resourceVersion: "40165"
  selfLink: /apis/compliance.openshift.io/v1alpha1/namespaces/openshift-compliance/rules/rhcos4-accounts-no-uid-except-zero
  uid: 6794bf39-630a-4592-9d00-10a4279d14fb
rationale: An account has root authority if it has a UID of 0. Multiple accounts&#xA;with a UID of 0 afford more opportunity for potential intruders to&#xA;guess a password for a privileged account. Proper configuration of&#xA;sudo is recommended to afford multiple system administrators&#xA;access to root privileges in an accountable manner.
severity: high
title: Verify Only Root Has UID 0
```

The attributes describe what the rule does (`description`), why are we checking
this rule (`rationale`), how important it is to remediate the rule in case it
fails (`severity`). The metadata include some operational attributes as well
as a list of controls the rule covers (`control.compliance.openshift.io/NIST-800-53`,
`policies.open-cluster-management.io/controls`, the latter is used by RHACM).
You can inspect all the rules in the profile like this to get a better idea of
what the profile covers.

We've determined which profile we're going to scan with and what the
profile actually does. Before actually creating the scan, we also need to
think about settings of the scan - how often we want to run it, what amount
of storage to dedicate to the scan results, and so on. This is what the
`ScanSetting` object is for. Let's see an example:
```
$ cat << EOF > periodic-setting.yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: periodic-setting
  namespace: openshift-compliance
schedule: "0 1 * * *"
rawResultStorage:
    size: "2Gi"
    rotation: 5
roles:
  - worker
  - master
EOF
```

The `ScanSetting` object helps you define properties such as:
 * which nodes should be scanned, expressed as the node roles
 * how much storage will be allocated for the results
 * what is the retention policy for the results
 * will the scan run periodically, and if yes, how often

To display more details about the `ScanSetting` object, run `oc explain scansettings`,
similarly you can also let `oc` explain any of the nested objects, e.g.
`oc explain scansettings.rawResultStorage`.

The example above would scan all nodes with role `master` or `worker`, allocate
2Gi of storage for each of the master and worker scans, keeping the last five
full scan results. In addition, the scan would run at 1:00AM every day.
The `ScanSetting` object is decoupled from the actual scan definition so that
you can share and reuse the same settings for different scans that
might themselves use different profiles.

The scan itself, or the intent of the scan is expressed using the
`ScanSettingBinding` object. The following example shows an example
of defining the scan for the `e8` profile we were exploring earlier:
```
$ cat << EOF > periodic-e8.yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: periodic-e8
  namespace: openshift-compliance
profiles:
  # Node checks
  - name: rhcos4-e8
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
  # Platform checks
  - name: ocp4-e8
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
settingsRef:
  name: periodic-setting
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
EOF
```

There are two important pieces of a `ScanSettingBinding` object:

* **profiles**: Contains a list of (`name,kind,apiGroup`) tuples that make up
  a selection of the `Profile` (or a `TailoredProfile` that we will explain later)
  to scan your environment with.
* **settingsRef**: A reference to a `ScanSetting` object also using the
  (`name,kind,apiGroup`) tuple that references the operational constraints.

Save both the `ScanSetting` and the `ScanSettingBinding` manifests to a
file and create them:
```
$ oc create -f periodic-setting.yaml
scansetting.compliance.openshift.io/periodic-setting created
$ oc create -f periodic-e8.yaml
scansettingbinding.compliance.openshift.io/periodic-e8 created
```

> **NOTE**
> 
> Using the [`oc-compliance`](https://github.com/JAORMX/oc-compliance) plugin 
> it's also possible to create `ScanSettingBindings` using the subcommand
> `oc compliance bind`. For this example, the invocation would have been:
> 
> ```
> $ oc compliance bind --name periodic-e8 --settings periodic-setting \
>     profile/rhcos4-e8 profile/ocp4-e8
> ```
>
> For more information on this command, do:
> ```
> $ oc compliance bind -h
> ```


The Compliance Operator takes these two objects and generates a
`ComplianceSuite` object that references a `ComplianceScan` object for each
role of a Node scan and one for the Platform scan:
```
$ oc get compliancesuite -nopenshift-compliance
NAME                                  PHASE     RESULT
periodic-e8   			      RUNNING   NOT-AVAILABLE
$ oc get compliancescan -nopenshift-compliance
NAME                     PHASE     RESULT
ocp4-e8                  DONE      NON-COMPLIANT
rhcos4-e8-master         RUNNING   NOT-AVAILABLE
rhcos4-e8-worker         RUNNING   NOT-AVAILABLE
```
You can think of `ComplianceSuite` as a collection of `ComplianceScan`
objects with an aggregated status.

Now our scans are up and running. The scans go through several
phases (`LAUNCHING`, `RUNNING`, `AGGREGATING` and `DONE`). This can
take up to several minutes. The phase and result are tracked by the
operator in the `status` subresource and look like this:
```
Status:
  Phase:   DONE
  Result:  NON-COMPLIANT
  Results Storage:
    Name:       rhcos4-e8-worker
    Namespace:  openshift-compliance
```

Eventually, it is expected that the scans converge in the `DONE`
phase and all the results would be `NON-COMPLIANT`. It might be useful
to know that events are issued for the scans and the suites in case they
reach a result. You can view the events with `oc describe compliancescan`,
for example:
```
Events:
  Type    Reason           Age   From      Message
  ----    ------           ----  ----      -------
  Normal  ResultAvailable  39m   scanctrl  ComplianceScan's result is: NON-COMPLIANT
```

You can also use `oc get events`:
```
$ oc get events --field-selector reason=ResultAvailable -nopenshift-compliance
LAST SEEN   TYPE     REASON            OBJECT                            MESSAGE
4m57s       Normal   ResultAvailable   compliancescan/ocp4-e8            ComplianceScan's result is: NON-COMPLIANT
4m2s        Normal   ResultAvailable   compliancesuite/periodic-e8       The result is: NON-COMPLIANT
4m2s        Normal   ResultAvailable   scansettingbinding/periodic-e8    The result is: NON-COMPLIANT
4m27s       Normal   ResultAvailable   compliancescan/rhcos4-e8-master   ComplianceScan's result is: NON-COMPLIANT
4m2s        Normal   ResultAvailable   compliancescan/rhcos4-e8-worker   ComplianceScan's result is: NON-COMPLIANT
```

Once the scans are finished, we can take a look at the scan results.
Those are represented as the CustomResource `compliancecheckresult`
and labeled with the scan name:
```
$ oc get compliancecheckresults -nopenshift-compliance -lcompliance.openshift.io/scan-name=ocp4-e8
$ oc get compliancecheckresults -nopenshift-compliance -lcompliance.openshift.io/scan-name=rhcos4-e8-master
$ oc get compliancecheckresults -nopenshift-compliance -lcompliance.openshift.io/scan-name=rhcos4-e8-worker
```

The attributes of the `ComplianceCheckResult` columns contain the result
of the check (typically `PASS` or `FAIL`) and the severity of the check.
The metadata links the check with the scan or suite through labels. The 
labels also contain the check result and severity so that you can filter
only checks with a certain result or severity:
```
$ oc get compliancecheckresults -lcompliance.openshift.io/scan-name=ocp4-e8,compliance.openshift.io/check-status=FAIL
$ oc get compliancecheckresults -lcompliance.openshift.io/scan-name=ocp4-e8,compliance.openshift.io/check-severity=medium
```

Some failing checks must be remediated manually, while some can be
remediated automatically. Those that can be remediated automatically have
a corresponding `ComplianceRemediation` object created. The next chapter
deals with remediations in detail.

While the `ComplianceCheckResult` objects provide a useful abstraction and
allow you to browse the results quickly, some organizations or auditors might
require the gory details. OpenSCAP provides raw results in a format called ARF
(Asset Reporting Format), that's often used to import the results into other
third-party tools. The ARF results are uploaded from the pods that actually
perform the scans to a `PersistentVolume` and rotated according to the
`rawResultStorage.rotation` parameter of the `ScanSetting` object. To see what
`PersistentVolumeClaims` store the results, inspect the scan statuses:
```
$ oc get compliancescans -o json | jq '.items[].status.resultsStorage'
{
  "name": "ocp4-e8",
  "namespace": "openshift-compliance"
}
{
  "name": "rhcos4-e8-master",
  "namespace": "openshift-compliance"
}
{
  "name": "rhcos4-e8-worker",
  "namespace": "openshift-compliance"
}
```

You can then view the individual PVCs:
```
$ oc get pvc rhcos4-e8-master
NAME               STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
rhcos4-e8-master   Bound    pvc-494a261b-9983-4d1a-abf3-b823e1a528a0   2Gi        RWO            gp2            83m
```

To fetch the results, define and spawn a pod that mounts the PVC and
sleeps, then copy the files out of the PVC to a local filesystem.
The pod definition could look like this:

```
$ cat << EOF > pv-extract.yaml
apiVersion: "v1"
kind: Pod
metadata:
  name: pv-extract
spec:
  containers:
    - name: pv-extract-pod
      image: registry.access.redhat.com/ubi8/ubi
      command: ["sleep", "3000"]
      volumeMounts:
        - mountPath: "/master-scan-results"
          name: master-scan-vol
  volumes:
    - name: master-scan-vol
      persistentVolumeClaim:
        claimName: rhcos4-e8-master
EOF
```

Copy this manifest, define the pod, then copy the results:
```
$ oc create -f pv-extract.yaml
pod/pv-extract created
$ oc cp pv-extract:/master-scan-results ./extract_results_dir
tar: Removing leading `/' from member names
```
The results are stored in directories numbered sequentially with the
number of the scan, up to the rotation policy, then reused:
```
$ ls
0  lost+found
$ ls 0 
openscap-pod-9294a45c73ef807cf82327f147f061fe3833eab7.xml.bzip2  openscap-pod-c41c6ef35a2ed0e442ae209120013ae708417c13.xml.bzip2  openscap-pod-e3f56090e7127d8499113d5188e2a83c18060007.xml.bzip2
```


Note that spawning a pod that mounts the Persistent Volume will keep the claim
as **Bound**. If the volume’s storage class that you’re using is
`ReadWriteOnce`, the volume is only mountable by one pod at a time. For this
reason, it’s important to delete the pod afterwards, since that way, when
running a subsequent scan, it’ll be possible for the operator to just schedule
a pod and keep storing the results there.

So, just do:

```
$ oc delete pod pv-extract
```

Once you’re done with the extraction.

> **NOTE**
> 
> Using the [`oc-compliance`](https://github.com/JAORMX/oc-compliance) plugin 
> it's also possible to extract the compliance results using the subcommand
> `oc compliance fetch-raw`. For this example, the invocation would have been:
> 
> ```
> $ mkdir results
> $ oc compliance fetch-raw scansettingbindings periodic-e8 -o results
> ```
> This will fetch the raw results to the directory called `results` and clean up
> by itself.
>
> For more information on this command, do:
> ```
> $ oc compliance fetch-raw -h
> ```

***

We can now move on to [working with remediations](04-remediations.md)
