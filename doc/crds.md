## The Custom Resource Definitions

The compliance operator uses several objects as CRDs in order to operate and output
results. We'll go through these in this document.

### The `ComplianceSuite` object

The API resource that you'll be interacting with is called `ComplianceSuite`.
It is a collection of `ComplianceScan` objects, each of which describes
a scan. In addition, the `ComplianceSuite` also contains an overview of the
remediations found and the statuses of the scans. For node scans, you'll typically want
to map a scan to a `MachinePool`, mostly because the remediations for any
issues that would be found contain `MachineConfig` objects that must be
applied to a pool.

A `ComplianceSuite` will look similar to this:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceSuite
metadata:
  name: fedramp-moderate
spec:
  autoApplyRemediations: false
  schedule: "0 1 * * *"
  scans:
    - name: workers-scan
      scanType: Node
      profile: xccdf_org.ssgproject.content_profile_moderate
      content: ssg-rhcos4-ds.xml
      contentImage: quay.io/complianceascode/ocp4:latest
      rule: "xccdf_org.ssgproject.content_rule_no_netrc_files"
      nodeSelector:
        node-role.kubernetes.io/worker: ""
status:
  Phase: DONE
  Result: NON-COMPLIANT
  scanStatuses:
  - name: workers-scan
    phase: DONE
    result: NON-COMPLIANT
```
In the `spec`:
* **autoApplyRemediations**: Specifies if any remediations found from the
  scan(s) should be applied automatically.
* **schedule**: Defines how often should the scan(s) be run in cron format.
* **scans** contains a list of scan specifications to run in the cluster.

In the `status`:
* **Phase**: indicates the overall phase where the scans are at. To
  get the results you normally have to wait for the overall phase to be `DONE`.
* **Result**: Is the overall verdict of the suite.
* **scanStatuses**: Will contain the status for each of the scans that the
  suite is tracking.

The suite in the background will create as many `ComplianceScan` objects as you
specify in the `scans` field. The fields will be described in the section
referring to `ComplianceScan` objects.

Note that `ComplianceSuites` will generate events which you can fetch
programmatically. For instance, to get the events for the suite called
`example-compliancesuite` you could use the following command:

```
oc get events --field-selector involvedObject.kind=ComplianceSuite,involvedObject.name=example-compliancesuite
LAST SEEN   TYPE     REASON            OBJECT                                    MESSAGE
23m         Normal   ResultAvailable   compliancesuite/example-compliancesuite   ComplianceSuite's result is: NON-COMPLIANT
```

This will also show up in the output of the `oc describe` command.

### The `ComplianceScan` object

Similarly to `Pods` in Kubernetes, a `ComplianceScan` is the base object that
the compliance-operator introduces. Also similarly to `Pods`, you normally
don't want to create a `ComplianceScan` object directly, and would instead want
a `ComplianceSuite` to manage it.

Note that several of the attributes in the `ComplianceScan` object are
OpenSCAP specific and require some degree of knowledge of the OpenSCAP
content being used. For better discoverability, please refer to the `Profile`
and `ProfileBundle` objects which provide information about available
OpenSCAP profiles and their attributes. In addition, the `ScanSetting` and
`ScanSettingBinding` objects provider a high level way to define scans by
referring to the `Profile` or `TailoredProfile` objects.

Let's look at an example:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceScan
metadata:
  name: worker-scan
spec:
  scanType: Node
  profile: xccdf_org.ssgproject.content_profile_moderate
  content: ssg-ocp4-ds.xml
  contentImage: quay.io/complianceascode/ocp4:latest
  rule: "xccdf_org.ssgproject.content_rule_no_netrc_files"
  nodeSelector:
    node-role.kubernetes.io/worker: ""
status:
  phase: DONE
  result: NON-COMPLIANT
```

In the `spec`:

* **scanType**: Is the type of scan to execute. There are currently two types:
  - **Node**: Is meant to run on the host that runs the containers. This spawns
    a privileged pod that mounts the host's filesystem and executes security
    checks directly on it.
  - **Platform**: Is meant to run checks related to the platform itself (the
    Kubernetes distribution). This will run a non-privileged pod that will
    fetch resources needed in the checks and will subsequently run the scan.
* **profile**: Is the XCCDF identifier of the profile that you want to run. A
  profile is a set of rules that check for a specific compliance target. In the
  example `xccdf_org.ssgproject.content_profile_moderate` checks for rules
  required in the NIST SP 800-53 moderate profile.
* **contentImage**: The security checklist definition or datastream
  (the XCCDF/SCAP file) will need to come from a container image. This is
  where the image is specified.
* **rule**: Optionally, you can tell the scan to run a single rule. This rule
  has to be identified with the XCCDF ID, and has to belong to the specified
  profile. Note that you can skip this parameter, and if so, the scan will run
  all the rules available for the specified profile.
* **nodeSelector**: For `Node` scan types, you normally want to encompass a
  specific type of node, this is achievable by specifying the `nodeSelector`.
  If you're running on OpenShift and want to generate remediations, this label
  has to match the label for the `MachineConfigPool` that the `MachineConfig`
  remediation will be created for. Note that if this parameter is not
  specified or doesn't match a `MachineConfigPool`, a scan will still be run,
  but remediations won't be created.
* **rawResultStorage.size**: Specifies the size of storage that should be asked
  for in order for the scan to store the raw results. (Defaults to 1Gi)
* **rawResultStorage.rotation**: Specifies the amount of scans for which the raw
  results will be stored. Older results will get rotated, and it's the
  responsibility of administrators to store these results elsewhere before
  rotation happens. Note that a rotation policy of '0' disables rotation
  entirely. Defaults to 3.
* **rawResultStorage.storageClassName**: Specifies the storage class that
  should be asked for in order for the scan to store the raw results. Not
  specifying this value will use the default storage class configured in the
  cluster. (Defaults to nil)
* **rawResultStorage.pvAccessModes**: Specifies the access modes for creating
  the PVC that will host the raw results from the scan. Please check the values
  that the storage class supports before setting this. Else, just use the default.
  (Defaults to ["ReadWriteOnce"])
* **scanTolerations**: Specifies tolerations that will be set in the scan Pods
  for scheduling. Defaults to allowing the scan to run on master nodes. For
  details on tolerations, see the
  [Kubernetes documentation on this](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/).

Regarding the `status`:

* **phase**: Indicates the phase the scan is at. You have to wait
  for a scan to be in the state `DONE` to get the results.
* **result**: Indicates the verdict of the scan. The scan can be `COMPLIANT`,
  `NON-COMPLIANT`, or report an `ERROR` if an unforeseen issue happened or
  there's an issue in the scan specification.
* **warnings**: Indicates non-fatal errors in the scan. e.g. the operator not having
  the necessary RBAC permissions to fetch a resource, or a resource type not existing
  in the cluster.

When a scan is created by a suite, the scan is owned by it. Deleting a
`ComplianceSuite` object will result in deleting all the scans that it created.

Once a scan has finished running it'll generate the results as Custom Resources
of the `ComplianceCheckResult` kind. However, the raw results in ARF format
will also be available. These will be stored in a Persistent Volume which has a
Persistent Volume Claim associated that has the same name as the scan.

Note that `ComplianceScans` will generate events which you can fetch
programmatically. For instance, to get the events for the suite called
`workers-scan` you could use the following command:

```
oc get events --field-selector involvedObject.kind=ComplianceScan,involvedObject.name=workers-scan
LAST SEEN   TYPE     REASON            OBJECT                        MESSAGE
25m         Normal   ResultAvailable   compliancescan/workers-scan   ComplianceScan's result is: NON-COMPLIANT
```

This will also show up in the output of the `oc describe` command.

### The `ComplianceCheckResult` object

When running a scan with a specific profile, several rules in the profiles will
be verified. For each of these rules, we'll have a `ComplianceCheckResult`
object that represents the state of the cluster for a specific rule.

Such an object looks as follows:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceCheckResult
metadata:
  labels:
    compliance.openshift.io/check-severity: medium
    compliance.openshift.io/check-status: FAIL
    compliance.openshift.io/suite: example-compliancesuite
    compliance.openshift.io/scan-name: workers-scan
  name: workers-scan-no-direct-root-logins
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ComplianceScan
    name: workers-scan
description: |-
  Direct root Logins Not Allowed
  Disabling direct root logins ensures proper accountability and multifactor
  authentication to privileged accounts. Users will first login, then escalate
  to privileged (root) access via su / sudo. This is required for FISMA Low
  and FISMA Moderate systems.
instructions: |-
  To ensure root may not directly login to the system over physical consoles,
  run the following command:
     cat /etc/securetty
  If any output is returned, this is a finding.
id: xccdf_org.ssgproject.content_rule_no_direct_root_logins
severity: medium
status: FAIL
```

Where:

* **description**: Contains a description of what's being checked and why
  that's being done. For checks that don't generate an automated remediation,
  contains the steps to remediate the issue if it's failing.
* **instructions**: How to evaluate if the rule status manually. If no automatic
  test is present, the rule status will be MANUAL and the administrator should
  follow these instructions.
* **id**: Contains a reference to the XCCDF identifier of the rule as it is in
  the data-stream/content.
* **severity**: Describes the severity of the check. The possible values are:
  `unknown`, `info`, `low`, `medium`, `high`
* **warnings**: A list of warnings that the user might want to look out for.
  Often, if the result is marked at NOT-APPLICABLE, a relevant warning will
  explain why.
* **status**: Describes the result of the check. The possible values are:
	* **PASS**: Which indicates that check ran to completion and passed.
	* **FAIL**: Which indicates that the check ran to completion and failed.
	* **INFO**: Which indicates that the check ran to completion and found
      something not severe enough to be considered error.
	* **MANUAL**: Which indicates that the check does not have a way to
        automatically assess success or failure and must be checked manually.
    * **INCONSISTENT**: Which indicates that different nodes report different
      results.
	* **ERROR**: Which indicates that the check ran, but could not complete
      properly.
	* **NOTAPPLICABLE**: Which indicates that the check didn't run because it is not
      applicable or not selected.

This object is owned by the scan that created it, as seen in the
`ownerReferences` field.

If a suite is running continuously (having the `schedule` specified) the
result will be updated as needed. For instance, an initial scan could have
determined that a check was failing, the issue was fixed, so a subsequent
scan would report that the check passes.

The `INCONSISTENT` status is specific to the operator and doesn't come from
the scanner itself. This state is used when one or several nodes differ
from the rest, which ideally shouldn't happen because the scans should
be targeting machine pools that should be identical. If an inconsistent
check is detected, the operator, apart from using the inconsistent state
does the following:
    * Adds the `compliance.openshift.io/inconsistent-check` label so that the
      inconsistent checks can be found easily
    * Tries to find the most common state and outliers from the common state and
      put this information into the `compliance.openshift.io/most-common-status`
      and `compliance.openshift.io/inconsistent-source` annotations.
    * Issues events for the Scan or the Suite

Unless the inconsistency is too big (e.g. one node passing and one skipping
a run), the operator still tries to create a remediation which should
allow to apply the remediation and move forward to a compliant state. If a
remediation is not created automatically, the administrator needs to make
the nodes consistent first before proceeding.

You can get all the check results from a suite by using the label 
`compliance.openshift.io/suite`

For instance:

```
oc get compliancecheckresults -l compliance.openshift.io/suite=example-compliancesuite
```

### The `ComplianceRemediation` object

For a specific check, it is possible that the data-stream (content) specified a
possible fix. If a fix applicable to Kubernetes is available, this will create
a `ComplianceRemediation` object, which identifies the object that can be
created to fix the found issue.

A remediation object can look as follows:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
  labels:
    compliance.openshift.io/suite: example-compliancesuite
    compliance.openshift.io/scan-name: workers-scan
    machineconfiguration.openshift.io/role: worker
  name: workers-scan-disable-users-coredumps
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ComplianceCheckResult
    name: workers-scan-disable-users-coredumps
    uid: <UID>
spec:
  apply: false
  object:
    current:
       apiVersion: machineconfiguration.openshift.io/v1
       kind: MachineConfig
       spec:
         config:
           ignition:
             version: 2.2.0
           storage:
             files:
             - contents:
                 source: data:,%2A%20%20%20%20%20hard%20%20%20core%20%20%20%200
               filesystem: root
               mode: 420
               path: /etc/security/limits.d/75-disable_users_coredumps.conf
    outdated: {}
```

Where:

* **apply**: Indicates whether the remediation should be applied or not.
* **object.current**: Contains the definition of the remediation, this object is
  what needs to be created in the cluster in order to fix the issue. Note that
  if `object.outdated` exists, this is not necessarily what is currently applied
  on the nodes due to the remediation being updated
* **object.outdated**: The remediation that was previously parsed from an earlier
  version of the content. The operator still retains the outdated objects to give
  the administrator a chance to review the new remediations before applying them.
  To take the new versions of the remediations to use, annotate the `ComplianceSuite`
  with the `compliance.openshift.io/remove-outdated` annotation. See also the
  troubleshooting document for more details.

Normally the objects need to be full Kubernetes object definitions, however,
there is a special case for `MachineConfig` objects. These are applied
per `MachineConfigPool` which are encompassed by a scan. The compliance
suite controller will, if remediations are to be applied automatically,
therefore pause the pool while the remediations are gathered in order to
give the remediations time to converge and speed up the remediation process.

This object is owned by the `ComplianceCheckResult` object, as seen in the
`ownerReferences` field.

You can get all the remediations from a suite by using the label 
`compliance.openshift.io/suite`

For instance:

```
oc get complianceremediations -l compliance.openshift.io/suite=example-compliancesuite
```

Not all `ComplianceCheckResult` objects create `ComplianceRemediation`
objects, only those that can be remediated automatically do. A
`ComplianceCheckResult` object has a related remediation if it's labeled
with the `compliance.openshift.io/automated-remediation` label, the
name of the remediation is the same as the name of the check. To list all
failing checks that can be remediated automatically, call:
```
oc get compliancecheckresults -l 'compliance.openshift.io/check-status in (FAIL),compliance.openshift.io/automated-remediation'
```
and to list those that must be remediated manually:
```
oc get compliancecheckresults -l 'compliance.openshift.io/check-status in (FAIL),!compliance.openshift.io/automated-remediation'
```
The manual remediation steps are typically stored in the `ComplianceCheckResult`'s
`description` attribute.

### The `ProfileBundle` object
OpenSCAP content for consumption by the Compliance Operator is distributed
as container images. In order to make it easier for users to discover what
profiles a container image ships, a `ProfileBundle` object can be created,
which the Compliance Operator then parses and creates a `Profile` object
for each profile in the bundle. The `Profile` can be then either used
directly or further customized using a `TailoredProfile` object.

An example `ProfileBundle` object looks like this:
```yaml
- apiVersion: compliance.openshift.io/v1alpha1
  kind: ProfileBundle
    name: ocp4
    namespace: openshift-compliance
  spec:
    contentFile: ssg-ocp4-ds.xml
    contentImage: quay.io/complianceascode/ocp4:latest
  status:
    dataStreamStatus: VALID
```

Where:

* **spec.contentFile**: Contains a path from the root directory (`/`) where
  the profile file is located
* **spec.contentImage**: A container image that encapsulates the profile files
* **status.dataStreamStatus**: Whether the Compliance Operator was able to parse
  the content files
* **status.errorMessage**: In case parsing of the content files fails, this
  attribute will contain a human-readable explanation.

The ComplianceAsCode upstream image is located at `quay.io/complianceascode/ocp4:latest`.
For OCP4, the two most used `contentFile` values would be `ssg-ocp4-ds.xml` which contain
the platform (Kubernetes) checks and `ssg-rhcos4-ds.xml` file which contains the node
(OS level) checks. For these two files, the corresponding `ProfileBundle` objects are created
automatically when the ComplianceOperator starts in order to provide useful defaults.
You can inspect the existing `ProfileBundle` objects by calling:

```
oc get profilebundle -nopenshift-compliance
```

### The `Profile` object
The `Profile` objects are never created manually, but rather based on a
`ProfileBundle` object, typically one `ProfileBundle` would result in
several `Profiles`. The `Profile` object contains parsed out details about
an OpenSCAP profile such as its XCCDF identifier, what kind of checks the
profile contains (node vs platform) and for what system or platform.

For example:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
description: |-
  This compliance profile reflects the core set of Moderate-Impact Baseline
  configuration settings for deployment of Red Hat Enterprise
  Linux CoreOS into U.S. Defense, Intelligence, and Civilian agencies.
...
id: xccdf_org.ssgproject.content_profile_moderate
kind: Profile
metadata:
  annotations:
    compliance.openshift.io/product: redhat_enterprise_linux_coreos_4
    compliance.openshift.io/product-type: Node
  creationTimestamp: "2020-07-14T16:18:47Z"
  generation: 1
  labels:
    compliance.openshift.io/profile-bundle: rhcos4
  name: rhcos4-moderate
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ProfileBundle
    name: rhcos4
    uid: 46be5b0f-e121-432e-8db2-3f417cdfdcc6
  resourceVersion: "101939"
  selfLink: /apis/compliance.openshift.io/v1alpha1/namespaces/openshift-compliance/profiles/rhcos4-moderate
  uid: ab64865d-811e-411f-9acf-7b09d45c1746
rules:
- rhcos4-account-disable-post-pw-expiration
- rhcos4-accounts-no-uid-except-zero
- rhcos4-audit-rules-dac-modification-chmod
- rhcos4-audit-rules-dac-modification-chown
- rhcos4-audit-rules-dac-modification-fchmod
- rhcos4-audit-rules-dac-modification-fchmodat
- rhcos4-audit-rules-dac-modification-fchown
- rhcos4-audit-rules-dac-modification-fchownat
- rhcos4-audit-rules-dac-modification-fremovexattr
...
title: NIST 800-53 Moderate-Impact Baseline for Red Hat Enterprise Linux CoreOS
```
Note that this example has been abbreviated, the full list of rules and the full description
are too long to display.

Notable attributes:

* **id**: The XCCDF name of the profile. Use this identifier when defining a `ComplianceScan`
  object as the value of the `profile` attribute of the scan.
* **rules**: A list of rules this profile contains. Each rule corresponds to a single check.
* **metadata.annotations.compliance.openshift.io/product-type**: Either `Node` or `Platform`.
  `Node`-type profiles scan the cluster nodes, `Platform`-type profiles scan the Kubernetes
  platform. Match this value with the `scanType` attribute of a `ComplianceScan` object.
* **metadata.annotations.compliance.openshift.io/product**: The name of the product this profile
  is targeting. Mostly for informational purposes.

Example usage:
```
# List all available profiles
oc get profile.complliance -nopenshift-compliance
# List all profiles generated from the rhcos4 profile bundle
oc get profile.compliance -nopenshift-compliance -lcompliance.openshift.io/profile-bundle=rhcos4
```

### The `Rule` object
As seen in the `Profile` object description, each profile contains a rather large number
of rules. An example `Rule` object looks like this:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
description: '<br /><br /><pre>$ sudo nmcli radio wifi off</pre>Configure the system
  to disable all wireless network interfaces with the&#xA;following command:'
id: xccdf_org.ssgproject.content_rule_wireless_disable_interfaces
kind: Rule
metadata:
  annotations:
    compliance.openshift.io/rule: wireless-disable-interfaces
    control.compliance.openshift.io/NIST-800-53: AC-18(a);AC-18(3);CM-7(a);CM-7(b);CM-6(a);MP-7
    policies.open-cluster-management.io/controls: AC-18(a),AC-18(3),CM-7(a),CM-7(b),CM-6(a),MP-7
    policies.open-cluster-management.io/standards: NIST-800-53
  labels:
    compliance.openshift.io/profile-bundle: rhcos4
  name: rhcos4-wireless-disable-interfaces
  namespace: openshift-compliance
  ownerReferences:
  - apiVersion: compliance.openshift.io/v1alpha1
    blockOwnerDeletion: true
    controller: true
    kind: ProfileBundle
    name: rhcos4
    uid: 46be5b0f-e121-432e-8db2-3f417cdfdcc6
  resourceVersion: "102322"
  selfLink: /apis/compliance.openshift.io/v1alpha1/namespaces/openshift-compliance/rules/rhcos4-wireless-disable-interfaces
  uid: 8debde1b-e2df-4058-a345-151905769187
  severity: medium
  rationale: The use of wireless networking can introduce many different attack vectors
  into&#xA;the organization&#39;s network. Common attack vectors such as malicious
  association&#xA;and ad hoc networks will allow an attacker to spoof a wireless access
  point&#xA;(AP), allowing validated systems to connect to the malicious AP and enabling
  the&#xA;attacker to monitor and record network traffic. These malicious APs can
  also&#xA;serve to create a man-in-the-middle attack or be used to create a denial
  of&#xA;service to valid network resources.
title: Deactivate Wireless Network Interfaces
```

As you can see, the Rule object contains mostly informational data. Some
attributes that might be directly usable to admins include `id` which can
be used as the value of the `rule` attribute of the `ComplianceScan` object
or the annotations that contain compliance controls that are addressed by
this rule.

### The `TailoredProfile` object
While we strive to make the default profiles useful in general, each organization might
have different requirements and thus might need to customize the profiles. This is where
the `TailoredProfile` is useful. It allows you to enable or disable rules, set variable
values and provide justification for these customizations. The `TailoredProfile`, upon
validating, creates a `ConfigMap` that can be referenced by a `ComplianceScan`. A more
user-friendly way of consuming the `TailoredProfile` way is to reference it directly
in a `ScanSettingBinding` object which is described later.

An example `TailoredProfile` that extends an RHCOS4 profile and disables a single rule
is displayed below:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: TailoredProfile
metadata:
  name: rhcos4-with-usb
spec:
  extends: rhcos4-moderate
  title: RHCOS4 moderate profile that allows USB support
  disableRules:
    - name: rhcos4-grub2-nousb-argument
      rationale: We use USB peripherals in our environment
status:
  id: xccdf_compliance.openshift.io_profile_rhcos4-with-usb
  outputRef:
    name: rhcos4-with-usb-tp
    namespace: openshift-compliance
  state: READY
```

Notable attributes:

* **spec.extends**: Name of the `Profile` object that this `TailoredProfile` builds upon
* **spec.title**: Human-readable title of the `TailoredProfile`
* **spec.disableRules**: A list of `name` and `rationale` pairs. Each name refers to a name
  of a `Rule` object that is supposed to be disabled. `Rationale` is a human-readable text
  describing why the rule is disabled.
* **spec.enableRules**: Equivalent of `disableRules`, except enables rules that might be
  disabled by default.
* **status.id**: The XCCDF ID of the resulting profile. Use variable when
  defining a `ComplianceScan` using this `TailoredProfile` as the value of the `profile`
  attribute of the scan.
* **status.outputRef.name**: The result of creating a `TailoredProfile` is typically a
  `ConfigMap`. This is the name of the `ConfigMap` which can be used as the value of the
  `tailoringConfigMap.name` attribute of a `ComplianceScan`.
* **status.state**: Either of `PENDING`, `READY` or `ERROR`. If the state is `ERROR`, the
  attribute `status.errorMessage` contains the reason for the failure.

### The `ScanSetting` and `ScanSettingBinding` objects
Defining the `ComplianceSuite` objects manually including all the details
such as XCCDF includes declaring a fair amount of attributes and therefore
creating the objects might be error-prone. In order to address the usual
cases in a more user-friendly manner, the Compliance Operator includes two
more objects that allow the user to define the compliance requirements on
a more high level.  In particular, the `ScanSettingBinding` object allows
to specify the compliance requirements by referencing the `Profile`
or `TailoredProfile` objects and the `ScanSetting` objects supplies
operational constraints such as schedule or which node roles must be
scanned. The Compliance Operator then generates the `ComplianceSuite`
objects based on the `ScanSetting` and `ScanSettingBinding` including all
the low-level details read directly from the profiles.

Let's inspect an example, first a `ScanSettingBinding` object:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: my-companys-compliance-requirements
profiles:
  # Node checks
  - name: rhcos4-with-usb
    kind: TailoredProfile
    apiGroup: compliance.openshift.io/v1alpha1
  # Cluster checks
  - name: ocp4-moderate
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
settingsRef:
  name: my-companys-constraints
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
```

There are two important pieces of a `ScanSettingBinding` object:

* **profiles**: Contains a list of (`name,kind,apiGroup`) triples that make up
  a selection of `Profile` of `TailoredProfile` to scan your environment with.
* **settingsRef**: A reference to a `ScanSetting` object also using the
  (`name,kind,apiGroup`) triple that prescribes the operational constraints
  like schedule or the storage size.

Next, let's look at an example of a `ScanSetting` object:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: my-companys-constraints
# Suite-specific settings
autoApplyRemediations: false
schedule: "0 1 * * *"
# Scan-specific settings
rawResultStorage:
  size: "2Gi"
  rotation: 10
# For each role, a separate scan will be created pointing
# to a node-role specified in roles
roles:
  - worker
  - master
```

The `ScanSetting` complements the `ScanSettingBinding` in the sense that the binding object
provides a list of suites, the setting object provides settings for the suites and scans
and places the node-level scans onto node roles. A single `ScanSetting` object can also
be reused for multiple `ScanSettingBinding` objects.

The following attributes can be set in the `ScanSetting:
* **autoApplyRemediations**: Specifies if any remediations found from the
  scan(s) should be applied automatically.
* **autoUpdateRemediations**: Defines whether or not the remediations
  should be updated automatically in case the content updates.
* **schedule**: Defines how often should the scan(s) be run in cron format.
* **rawResultStorage.size**: Specifies the size of storage that should be asked
  for in order for the scan to store the raw results. (Defaults to 1Gi)
* **rawResultStorage.rotation**: Specifies the amount of scans for which the raw
  results will be stored. Older results will get rotated, and it's the
  responsibility of administrators to store these results elsewhere before
  rotation happens. Note that a rotation policy of '0' disables rotation
  entirely. Defaults to 3.
* **scanTolerations**: Specifies tolerations that will be set in the scan Pods
  for scheduling. Defaults to allowing the scan to run on master nodes. For
  details on tolerations, see the
  [Kubernetes documentation on this](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/).
 * **roles**: Specifies the `node-role.kubernetes.io` label value that any Node Suite
  should be scheduled on. Internally, each `Profile` or `TailoredProfile` in a
   `ScanSettingBinding` creates a `ComplianceScan` for each role specified in this attribute.

 When the above objects are created, the result are a suite and three scans:
```
$ oc get compliancesuites
NAME                                  PHASE     RESULT
my-companys-compliance-requirements   RUNNING   NOT-AVAILABLE
$ oc get compliancescans
NAME                     PHASE     RESULT
ocp4-moderate            DONE      NON-COMPLIANT
rhcos4-with-usb-master   RUNNING   NOT-AVAILABLE
rhcos4-with-usb-worker   RUNNING   NOT-AVAILABLE
```

If you examine the scans, you'll see that they automatically pick up the
correct XCCDF scan ID as well as the tailoring configMap without having to
specify these low-level details manually. The suite is also owned by the
 `ScanSettingBinding`, meaning that if you delete the binding, the suite also
 gets deleted.
