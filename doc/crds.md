# The Custom Resource Definitions

The Compliance Operator introduces several CRDs to aid users in their
compliance scanning.

![CRDs](images/co-crds.png?raw=true "CRDs")

The main workflow is:

* Define what you need to comply with.
* Define how you want your scans to be configured.
* Link the what with the how.
* Track your compliance scans
* View the results

We'll go through the elements.

## What do you need to comply with?

In order to effectuate compliance scans, the Compliance Operator uses pre-built
security content which comes from the [ComplianceAsCode](complianceascode.readthedocs.io/)
community. This is exposed by the Compliance Operator as custom resources
which allow us to define the profiles we need to comply with and set
relevant parameters.

### The `ProfileBundle` object
OpenSCAP content for consumption by the Compliance Operator is distributed
as container images. In order to make it easier for users to discover what
profiles a container image ships, a `ProfileBundle` object can be created,
which the Compliance Operator then parses and creates a `Profile` object
for each profile in the bundle. The Compliance Operator will also parse
`Rule` and `Variable` objects which are used by the `Profiles` in order
for system administrators to have the full view of what's contained
in the profile. The `Profile` can be then either used directly or
further customized using a `TailoredProfile` object.

An example `ProfileBundle` object looks like this:
```yaml
- apiVersion: compliance.openshift.io/v1alpha1
  kind: ProfileBundle
    name: ocp4
    namespace: openshift-compliance
  spec:
    contentFile: ssg-ocp4-ds.xml
    contentImage: quay.io/compliance-operator/compliance-operator-content:latest
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

The ComplianceAsCode upstream image is located at `quay.io/compliance-operator/compliance-operator-content:latest`.
For OCP4, the two most used `contentFile` values would be `ssg-ocp4-ds.xml` which contain
the platform (Kubernetes) checks and `ssg-rhcos4-ds.xml` file which contains the node
(OS level) checks. For these two files, the corresponding `ProfileBundle` objects are created
automatically when the ComplianceOperator starts in order to provide useful defaults.
You can inspect the existing `ProfileBundle` objects by calling:

```
oc get profilebundle -nopenshift-compliance
```

Note that in case you need to roll back to a known-good content image
from an invalid image, the `ProfileBundle` might be stuck in the `PENDING`
state. A workaround is to move to a different image than the previous one.
Please see [this bug report](https://bugzilla.redhat.com/show_bug.cgi?id=1914279#c2)
for more details. Alternatively, you can delete and re-create the
`ProfileBundle` object to get it to a good state again.

The Compliance Operator usually ships with some valid `ProfileBundles`
so they're usable and parsed as soon as the operator is installed.

### The `Profile` object
The `Profile` objects are never created nor modified manually, but rather based on a
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
checkType: Platform
description: Use network policies to isolate traffic in your cluster network.
id: xccdf_org.ssgproject.content_rule_configure_network_policies_namespaces
instructions: |-
  Verify that the every non-control plane namespace has an appropriate
  NetworkPolicy.

  To get all the non-control plane namespaces, you can do the
  following command oc get  namespaces -o json | jq '[.items[] | select((.metadata.name | startswith("openshift") | not) and (.metadata.name | startswith("kube-") | not) and .metadata.name != "default")]'

  To get all the non-control plane namespaces with a NetworkPolicy, you can do the
  following command oc get --all-namespaces networkpolicies -o json | jq '[.items[] | select((.metadata.name | startswith("openshift") | not) and (.metadata.name | startswith("kube-") | not) and .metadata.name != "default") | .metadata.namespace] | unique'

  Make sure that the namespaces displayed in the commands of the commands match.
kind: Rule
metadata:
  annotations:
    compliance.openshift.io/rule: configure-network-policies-namespaces
    control.compliance.openshift.io/CIS-OCP: 5.3.2
    control.compliance.openshift.io/NERC-CIP: CIP-003-3 R4;CIP-003-3 R4.2;CIP-003-3
      R5;CIP-003-3 R6;CIP-004-3 R2.2.4;CIP-004-3 R3;CIP-007-3 R2;CIP-007-3 R2.1;CIP-007-3
      R2.2;CIP-007-3 R2.3;CIP-007-3 R5.1;CIP-007-3 R6.1
    control.compliance.openshift.io/NIST-800-53: AC-4;AC-4(21);CA-3(5);CM-6;CM-6(1);CM-7;CM-7(1);SC-7;SC-7(3);SC-7(5);SC-7(8);SC-7(12);SC-7(13);SC-7(18)
  labels:
    compliance.openshift.io/profile-bundle: ocp4
  name: ocp4-configure-network-policies-namespaces
  namespace: openshift-compliance
rationale: Running different applications on the same Kubernetes cluster creates a
  risk of one compromised application attacking a neighboring application. Network
  segmentation is important to ensure that containers can communicate only with those
  they are supposed to. When a network policy is introduced to a given namespace,
  all traffic not allowed by the policy is denied. However, if there are no network
  policies in a namespace all traffic will be allowed into and out of the pods in
  that namespace.
severity: high
title: Ensure that application Namespaces have Network Policies defined.
```

As you can see, the Rule object contains mostly informational data. Some
attributes that might be directly usable to admins include `id` which can
be used as the value of the `rule` attribute of the `ComplianceScan` object
or the annotations that contain compliance controls that are addressed by
this rule.

Notable attributes:

* **id**: XCCDF identifier. Parsed directly from the datastream.
* **instructions**: Manual instructions to audit for this specific control.
* **rationale**: A textual description of why this rule is being checked.
* **severity**: A textual description of how severe is it to fail this rule.
* **title**: A small summary of what this rule does
* **checkType**: Indicates the type of check that this rule executes. `Node` is
  done directly on the node. `Platform` is done on the Kubernetes API layer. An
  empty value means there is no automated check and this will merely be
  informational.

Ownership:

Rules will have an appropriate label to easily identify the ProfileBundle that
created it. The profileBundle will also be specified in the OwnerReferences of
this object.

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

Another example that changes the value of a variable the content uses to check
for a kubelet eviction limit is below:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: TailoredProfile
metadata:
  name: cis-node-kubelet-memory
spec:
  extends: ocp4-moderate-node
  title: CIS node with different kubelet memory limit
  setValues:
    - name: upstream-ocp4-var-kubelet-evictionhard-memory-available
      rationale: Bump the kubelet hard eviction limit to 500MB
      value: 500Mi
```

Notable attributes:

* **spec.extends**: (Optional) Name of the `Profile` object that this `TailoredProfile` builds upon
* **spec.title**: Human-readable title of the `TailoredProfile`
* **spec.disableRules**: A list of `name` and `rationale` pairs. Each name refers to a name
  of a `Rule` object that is supposed to be disabled. `Rationale` is a human-readable text
  describing why the rule is disabled.
* **spec.enableRules**: Equivalent of `disableRules`, except enables rules that might be
  disabled by default.
* **spec.setValues**: Allows for setting specific values to something other
  than their current default.
* **status.id**: The XCCDF ID of the resulting profile. Use variable when
  defining a `ComplianceScan` using this `TailoredProfile` as the value of the `profile`
  attribute of the scan.
* **status.outputRef.name**: The result of creating a `TailoredProfile` is typically a
  `ConfigMap`. This is the name of the `ConfigMap` which can be used as the value of the
  `tailoringConfigMap.name` attribute of a `ComplianceScan`.
* **status.state**: Either of `PENDING`, `READY` or `ERROR`. If the state is `ERROR`, the
  attribute `status.errorMessage` contains the reason for the failure.

While it's possible to extend a profile and build it based on another one, it's also
possible to write a profile from scratch using the `TailoredProfile` construct.
To do this, remember to set an appropriate title and description. It's very important
to leave the `extends` field empty for this case. Subsequently, you'll also need
to indicate to the Compliance Operator what type of scan will this custom profile
generate:

* Node scan: Scans the Operating System.
* Platform scan: Scans the OpenShift configuration.

To do this, set the following annotation on the TailoredProfile object:

```
  compliance.openshift.io/product-type: <Type>
```

Where the `Platform` type will build a Platform scan, and `Node` will
build an OS scan. Note that if no `product-type` annotation is given, the
operator will default to `Platform`. Adding the `-node` suffix to the
name of the `TailoredProfile` object will have a similar effect as
adding the `Node` product type annotation, and will generate an Operating
System scan.

## How you want your scans to be configured?

The specifics of how a scan should happen, where should it happen, and how
often, are also something that's configurable for the Compliance Operator
using a custom resource

### The `ScanSetting` object

To easily allow administrators to define and re-use settings of how
they'd like the compliance scans to happen, the `ScanSetting` object
provides the necessary tunables.

A sample looks as follows:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: my-companys-constraints
autoApplyRemediations: false
autoUpdateRemediations: false
schedule: "0 1 * * *"
rawResultStorage:
  size: "2Gi"
  rotation: 10
# For each role, a separate scan will be created pointing
# to a node-role specified in roles
roles:
  - worker
  - master
```

The following attributes can be set in the `ScanSetting:

* **autoApplyRemediations**: Specifies if any remediations found from the
  scan(s) should be applied automatically.
* **autoUpdateRemediations**: Defines whether or not the remediations
  should be updated automatically in case the content updates.
* **schedule**: Defines how often should the scan(s) be run in cron format.
* **scanTolerations**: Specifies tolerations that will be set in the scan Pods
  for scheduling. Defaults to allowing the scan to ignore taints. For
  details on tolerations, see the
  [Kubernetes documentation on this](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/).
 * **roles**: Specifies the `node-role.kubernetes.io` label value that any scan of type `Node`
  should be scheduled on.
* **rawResultStorage.size**: Specifies the size of storage that should be asked
  for in order for the scan to store the raw results. (Defaults to 1Gi)
* **rawResultStorage.rotation**: Specifies the amount of scans for which the raw
  results will be stored. Older results will get rotated, and it's the
  responsibility of administrators to store these results elsewhere before
  rotation happens. Note that a rotation policy of '0' disables rotation
  entirely. Defaults to 3.
* **rawResultStorage.nodeSelector**: By setting this, it's possible to
  configure where the result server instances are run. These instances
  will mount a Persistent Volume to store the raw results, so special
  care should be taken to schedule these in trusted nodes.
* **rawResultStorage.tolerations**:  Specifies tolerations needed
  for the result server to run on the nodes. This is useful in
  case the target set of nodes have custom taints that don't allow certain
  workloads to run. Defaults to allowing scheduling on master nodes.
* **strictNodeScan**: Defines whether the scan should proceed if we're not able to
  scan all the nodes or not. `true` means that the operator
  should be strict and error out. `false` means that we don't
  need to be strict and we can proceed.

A single `ScanSetting` object can also be reused for multiple scans,
as it merely defines the settings.

The Compliance Operator creates two `ScanSetting` objects on startup:
 * **default**: a ScanSetting that would run a scan every day at 1AM on both masters and workers,
   using a 1GBi PV and keeping the last three results. Remediations are neither applied nor updated
   automatically.
 * **default-auto-apply**: As above, except both autoApplyRemediations and autoUpdateRemediations
   are set to true.

## Linking the "what" with the "how"

When an organization has defined the standard they need to comply with,
and thus selected a profile (or tailored one), and has also
defined the settings to run the scans, we can now link them together.

The Compliance Operator will do the right thing and make sure it happens.

### `ScanSettingBinding` objects
This object allows to specify the compliance requirements by
referencing the `Profile` or `TailoredProfile` objects. It is then
linked to a `ScanSetting` object which supplies operational
constraints such as schedule or which node roles must be
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

The `ScanSetting` complements the `ScanSettingBinding` in the sense that the binding object
provides a list of suites, the setting object provides settings for the suites and scans
and places the node-level scans onto node roles.

When the above objects are created, the result will be a compliance suite:
```
$ oc get compliancesuites
NAME                                  PHASE     RESULT
my-companys-compliance-requirements   RUNNING   NOT-AVAILABLE
```

If you examine the suite, you'll see that it automatically picks up the
correct XCCDF scan ID as well as the tailoring configMap without having to
specify these low-level details manually. The suite is also owned by the
 `ScanSettingBinding`, meaning that if you delete the binding, the suite also
 gets deleted.

## Tracking your compliance scans

The next thing we'll want to do is see how our scans are doing.

### The `ComplianceSuite` object

The `ComplianceSuite` contains the raw settings to create scans and the
overall result. For scans of type `Node`, you'll typically want
to map a scan to a `MachineConfigPool`, mostly because the remediations for any
issues that would be found contain `MachineConfig` objects that must be
applied to a pool. So, if you're specifying the label yourself,
make sure that it directly applies to a pool. When the `ComplianceSuite` is
created by a `ScanSettingBinding`, this will be done by the Compliance Operator.

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
      contentImage: quay.io/compliance-operator/compliance-operator-content:latest
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

**NOTE**: Defining the `ComplianceSuite` objects manually including all the details
such as XCCDF includes declaring a fair amount of attributes and therefore
creating the objects might be error-prone. 

### (Advanced) The `ComplianceScan` object

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
  contentImage: quay.io/compliance-operator/compliance-operator-content:latest
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

## Viewing the results

When a compliance suite gets to the `DONE` phase, we'll have results
and possible fixes (remediations) available.

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
 * **valuesUsed**: a list of settable variables associated with the rule scan result,
  a user can set these variables in a tailored profile.

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

