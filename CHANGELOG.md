# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic
Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

### Enhancements

- The `api-resource-collector` `ClusterRole` has been updated to fetch network
  resources for the `operator.openshift.io` API group. This is necessary to
  automate checks that ensure the cluster is using a CNI that supports network
  policies. Please refere to the
  [bug](https://bugzilla.redhat.com/show_bug.cgi?id=2072431) for more
  information.

- The `api-resource-collector` `ClusterRole` has been updated to fetch network
  resources for the `gitopsservices.pipelines.openshift.io` API group. This is
  necessary to automate checks that ensure the cluster is using GitOps operator.

### Fixes

- The compliance content images have moved to
  [compliance-operator/compliance-operator-content](https://quay.io/repository/compliance-operator/compliance-operator-content)
  Quay repository. This should be a transparent change for end users and fixes
  CI that relies on content for end-to-end testing.

- Fix issues of unpausing machine config pool too soon after applying remediations,
  added a check to check if kubeletconfig has been fully rendered into machine config
  before unpause affected pool. [1]
  ([1]https://bugzilla.redhat.com/show_bug.cgi?id=2071854).

### Internal Changes

- Added node resource to the list of resources we always fetch so that arch CPEs will
  be evaluated appropriately.

### Deprecations

-

### Removals

-

### Security

-

## [0.1.50] - 2022-04-05

### Enhancements

- Added necessary permessions for api-resource-collector so that the new rule
  `cluster_logging_operator_exist` can be evaluate properly. [1]
  [1] https://github.com/ComplianceAsCode/content/pull/8511

### Fixes

- 

### Removals

- 

## [0.1.49] - 2022-03-22

### Enhancements

- Restructured the project documentation into separate guides for different
  audiences. This primarily includes an installation guide, a usage guide, and
  a contributor guide.

### Fixes

- Added network resource to the list of resources we always fetch [1] so that network
  OVN/SDN CPEs will be able to verify if the cluster has an OVN/SDN network type.
  The CPEs have added here [2]. The SDN rules have been updated [3] to use SDN CPE,
  so that these rules will show correct results based on cluster network type.
  ([1](https://github.com/openshift/compliance-operator/pull/785)).
  ([2](https://github.com/ComplianceAsCode/content/pull/8134)).
  ([3](https://github.com/ComplianceAsCode/content/pull/8141)).
  ([bug](https://bugzilla.redhat.com/show_bug.cgi?id=1994609)).
- When a TailoredProfile transitions from Ready to Error, the corresponding
  ConfigMap is removed. This prevents the ConfigMap from being reused with
  obsolete data while the parent object is in fact marked with an error
- The ScanSettingBinding controller now reconciles TailoredProfile instances
  related to a ScanSettingBinding. This ensures that the controller can
  proceed with generating scans in case the binding used to point to
  TailoredProfile that had been marked with an error, but was subsequently
  fixed ([upstream issue 791](https://github.com/openshift/compliance-operator/issues/791))
- Node scans are only scheduled on nodes running Linux. This allows running
  scans on cluster with a mix of Linux and Windows nodes. ([RHBZ #2059611](https://bugzilla.redhat.com/show_bug.cgi?id=2059611))

### Internal Changes

- Implemented a multi-step release process to prevent unintentional changes
  from making their way into the release
  ([bug](https://github.com/openshift/compliance-operator/issues/783)).
- The TestInconsistentResult e2e test was relying on a certain order of results.
  This bug was fixed and the test now passes with any order of results.

### Removals

- The Deployment resource in `deploy/eks.yaml` has been removed in favor of a
  generic resource in the compliance-operator-chart Helm templates. The
  `deploy/eks.yaml` file conflicted with various development and build tools
  that assumed a single Deployment resource in the `deploy/` directory. Please
  use the Helm chart for
  [deploying](https://github.com/openshift/compliance-operator/blob/master/doc/install.md#deploying-with-helm)
  the operator on AWS EKS.


## [0.1.48] - 2022-01-28

### Enhancements

- The operator is now aware of other Kubernetes distributions outside of
  OpenShift to accommodate running the operator on other platforms. This allows
  `ScanSettings` to have different behaviors depending on the platform.
  `ScanSettings` will automatically schedule to `worker` and `master` nodes
  when running in an OpenShift cluster, maintaining the previous default
  behavior for OpenShift clusters. When running on AWS EKS, `ScanSettings` will
  schedule to all available nodes, by default. The operator will also inherit
  the `nodeSelector` and `tolerations` from the operator pod when running on
  EKS.
- Improved support for running the operator in
  [Hypershift](https://github.com/openshift/hypershift) environments by
  allowing the rules to load values at runtime. For example, loading in the
  `ConfigMap` based on different namespaces for each cluster, which affects the
  API resource path of the `ConfigMap`. Previous versions of the operator
  included hard-coded API paths, where now they can be loaded in from the
  compliance content, better enabling operator usage in Hypershift deployments.
- You can now install the operator using a Helm chart. This includes support
  for deploying on OpenShift and AWS EKS. Please see the
  [documentation](https://github.com/openshift/compliance-operator/#deploying-with-helm)
  on how to deploy the operator using Helm.
- Introduced a process and guidelines for writing release notes
  ([documentation](https://github.com/openshift/compliance-operator/#writing-release-notes))

### Fixes

- Improved rule parsing to check for extended OVAL definitions ([bug](https://bugzilla.redhat.com/show_bug.cgi?id=2040282))
  - Previous versions of the operator wouldn't process extended OVAL
    defintions, allowing some rules to have `CheckType=None`. This version
    includes support for processing extended defintions so `CheckType` is
    properly set.
- Properly detect `MachineConfig` ownership for `KubeletConfig` objects
  ([bug](https://bugzilla.redhat.com/show_bug.cgi?id=2040401))
  - Previous versions of the operator assumed that all `MachineConfig` objects
    were created by a `KubeletConfig`. In instances where a `MachineConfig` was
    not generated using a `KubeletConfig`, the operator would stall during
    remediation attempting to find the `MachineConfig` owner. The operator now
    gracefully handles `MachineConfig` ownership checks during the remediation
    process.
- Updated documentation `TailoredProfile` example to be consistent with
  `TailoredProfile` CRD
- Minor grammatical documentation updates.

## [0.1.47] - 2021-12-15
### Changes

 - enhancements
      - Add status descriptor for CRDs - the CRDs' status attributes were annotated
        with [status descriptors](https://github.com/openshift/console/blob/master/frontend/packages/operator-lifecycle-manager/src/components/descriptors/reference/reference.md)
        that would provide a nicer UI for the status subresources of Compliance Operator's
        CRs in the OpenShift web UI console.
      - ScanSetting: Introduce special `@all` role to match all nodes - Introduces
        the ability to detect `@all` in the ScanSettings roles. When used, the
        scansettingbinding controller will set an empty `nodeSelector` which
        will then match all nodes available. This is mostly intended for
        non-OpenShift deployments.
      - Inherit scheduling for workloads from Compliance Operator manager - This
        enhancement removed hardcoded assumptions that all nodes are labeled
        with 'node-role.kubernetes.io/'. The Compliance Operator was using these
        labels for scheduling workloads, which works fine in OpenShift, but not
        on other distributions. Instead, all controllers inherit the placement
        information (nodeSelector and tolerations) from the controller manager.
        This enhancement is mostly aimed at non-OpenShift distributions.
      - Enable defaults per platform - This enables the Compliance Operator
        to specify defaults per specific platform. At the moment, OpenShift and
        EKS are supported. For OpenShift, the ScanSettings created by defaults
        target master and worker nodes and allow the resultserver to be created
        on master nodes. For EKS, ScanSettings schedule on all available nodes
        and inherit the nodeSelector and tolerations from the operator.
 - bug fixes
      - Remove regex checks for url-encoded content - When parsing remediations,
        the Compliance Operator used to verify the remediation content using a
        too strict regular expression and would error out processing
        valid remediations. The bug was affecting sshd related remediations from
        the ComplianceAsCode project in particular. The check was not
        necessary and was removed. ([RHBZ #2033009](https://bugzilla.redhat.com/show_bug.cgi?id=2033009))
      - Fix bugs where kubeletconfig gets deleted when unapplying - a
        KubeletConfig remediation was supposed to be re-applied on a subsequent
        scan run (typically with `auto_apply_remediations=true`), the remediation
        might not be applied correctly, leading to some of the remediations
        not being applied at all. Because the root cause of the issue
        was in code that was handling unapplying KubeletConfig remediations, the
        unapplying of KubeletConfig remediations was disabled until a better fix
        is developed. ([RHBZ #2032420](https://bugzilla.redhat.com/show_bug.cgi?id=2032420))
 - internal changes
      - Add human-readable changelog for 0.1.45 and 0.1.46
      - Add documentation for E2E test

## [0.1.46] - 2021-12-01
### Changes
 - enhancements
     - Make showing `NotApplicable` results optional - the `ComplianceScan` and
       the `ScanSetting` CR were extended (through extending of a shared structure)
       with a new field `showNotApplicable` which defaults to `false`. When set
       to `false`, the Compliance Operator will not render
       `ComplianceCheckResult` objects that do not apply to the system being
       scanned, but are part of the benchmark, e.g.  rules that check for etcd
       properties on worker nodes.  When set to `true`, all checks results,
       including those not applicable would be created.
     - metrics: Add `ComplianceSuite` status gauge and alert - enables monitoring
       of Compliance Suite status through metrics.
       - Add the `compliance_operator_compliance_state` gauge metric that
         switches to 1 for a ComplianceSuite with a NON-COMPLIANT result,
         0 when COMPLIANT, 2 when INCONSISTENT, and 3 when ERROR.
       - Create a `PrometheusRule` warning alert for the gauge.
     - Support deployments on all namespaces - adds support for watching all
       namespaces by passing an empty value to the `WATCH_NAMESPACE`
       environment variable. Please note that the default objects
       (`ProfileBundles`, `ScanSettings`) are always only created in the operator's
       namespace.
 - bug fixes
     - Fix documentation for remediation templating - the `doc/remediation-templating.md`
       document was improved to reflect the current state of the remediation
       templating.
 - internal changes
     - Log creation of default objects - There were cases where we need
       to debug if our default objects have been created. It was non-trivial
       to figure this out from the current logs, so the logging was extended
       to include the creation of the default `ProfileBundle` and `ScanSetting`
       objects.


## [0.1.45] - 2021-10-28
### Changes
 - enhancements
     - Implement version applicability for remediations - remediations coming
       from the ComplianceAsCode project can now express their minimal requires
       Kubernetes or OpenShift versions. If the remediation is applied on a
       cluster that does not match the version requirement, such remediation
       would not be created. This functionality is used e.g. by the
       `rhcos4-configure-usbguard-auditbackend` rule, as seen from the
       `complianceascode.io/ocp-version: '>=4.7.0'` annotation on its fix.
     - Add "infrastructure" to resources we always fetch - this is an
       enhancement for content writers. The 'infrastructure/cluster' object
       is now always fetched, making it possible to determine the platform
       that CO is running at. This allows to support checks that are only
       valid for a certain cloud platform (e.g. only for AWS)
 - bug fixes
     - Add support for rendering variable in rule objects - if a
       ComplianceAsCode check uses a variable in a rule's description or
       rationale, the variable's value is now correctly rendered
     - Remove permissions for aggregator to list nodes - a previous version
       of the Compliance Operator assigned the permissions to list and get
       nodes to the aggregator pod. Those permissions were not needed and
       were removed.
     - Fix Kubernetes version dependency parsing bug - a bug in the version
       applicability for remediations. This is a bug introduced and fixed
       in this release.
 - internal changes
     - Add permissions to get and list machineset in preparation for
       implementation of req 3.4.1 pcidss - the RBAC rules were extended
       to support the PCI-DSS standard.
     - Add a more verbose Changelog for the recent versions

## [0.1.44] - 2021-10-20
### Changes
 - enhancements
     - Add option to make scan scheduling strict/not strict - adds a new
       ComplianceScan/Suite/ScanSetting option called strictNodeScan.
       This option defaults to true meaning that the operator will error
       out of a scan pod can't be scheduled. Switching the option to
       true makes the scan more permissive and go forward. Useful for
       clouds with ephemeral nodes.
     - Result Server: Make nodeSelector and tolerations configurable -
       exposes the nodeSelector and tolerations attributes through the
       ScanSettings object. This enables deployers to configure where
       the Result Server will run, and thus what node will host the
       Persistent Volume that will contain the raw results. This is needed
       for cases where the storage driver doesn't allow us to schedule
       a pod that makes use of a persistent volume on the master nodes.
 - bug fixes
     - Switch to using openscap 1.3.5 - The latest openscap version fixes
       a potential crash.
     - Create a kubeletconfig per pool - Previously, only one global
       KubeletConfig object would have been created. A per-pool KubeletConfig
       is created now.
 - internal changes
     - Add Vincent Shen to OWNERS file
     - e2e: Fix TestRulesAreClassifiedAppropriately test

## [0.1.43] - 2021-10-14
### Changes
 - enhancements
      - Add KubeletConfig Remediation Support - adds the needed logic
        for the Compliance Operator to remediate KubeletConfig objects.
 - bug fixes
      - none
 - internal changes
      - Update api-resource-collector comment
      - Add permission for checking the kubeadmin user
      - Add variable support for non-urlencoded content
      - Revamp CRD docs
      - Makefile: Make container image bundle depend on $TAG

## [0.1.42] - 2021-10-04
### Changes
 - enhancements
      - Add error to the result object as comment - For content developers.
        Allows to differentiate between objects that don't exist in
        the cluster versus objects that can't be fetched.
 - bug fixes
      - Validate that rules in tailored profile are of appropriate type -
        tightens the validation of TailoredProfiles so that only rules
        of the same type (Platform/Node) are included
      - Fix needs-review unpause pool - remediations that need a varible
        to be set have the NeedsReview state. When auto-applying remediations,
        these need to have all variables set before the MachineConfig pool
        can be unpaused and the remediations applied.
 - internal changes
      - Add description to TailoredProfile yaml
      - Fix error message json representation in CRD
      - Update 06-troubleshooting.md
      - Remove Benchmark unit tests
      - add openscap image build
      - aggregator: Remove MachineConfig validation
      - TailoredProfiles: Allocate rules map with expected number of items
      - Makefile: Add push-openscap-image target
      - docs: Document the default-auto-apply ScanSetting
      - Proposal for Kubelet Config Remediation

## [0.1.41] - 2021-09-20
### Changes
  - enhancements
      - Add instructions and check type to Rule object - The rule objects now
        contain two additional attributes, `checkType` and `description` that
        allow the user to see if the Rule pertains to a Node or a Platform
        check and allow the user to audit what the check represented by the
        Rule does.
      - Add support for multi-value variable templating - When templating
        remediations with variables, a multi-valued variable is expanded
        as per the template.
  - bug fixes
      - Specify fsgroup, user and non-root user usage in resultserver - when
        running on OpenShift, the user and fsgroup pod attributes are selected
        from namespace annotations. On other Kubernetes distributions,
        this wouldn't work. If Compliance Operator is not running on OpenShift,
        a hardcoded default is selected instead.
      - Fix value-required handling - Ensures that the set variable is read
        from the tailoring as opposed to reading it from the datastream
        itself. Thus, ensuring that we actually detect when a variable is
        set and allow the remediation to be created appropriately.
      - Use ClusterRole/ClusterRoleBinding for monitoring permissions - the
        RBAC Role and RoleBinding used for Prometheus metrics
        were changed to Cluster-wide to ensure that monitoring works out of
        the box.
  - internal changes
      - Gather /version when doing Platform scans
      - Add flag to skip the metrics deployment
      - fetch openscap version during build time
      - e2e: Mark logging functions as helper functions
      - Makefile: Rename IMAGE_FORMAT var

## [0.1.40] - 2021-09-09
### Changes
 - enhancements
      - Add support for remediation templating for operator - The Compliance
        Operator is now able to change remediations based on variables set
        through the compliance profile. This is useful e.g. for remediations
        that include deployment-specific values such as time outs, NTP server
        host names or similar. Note that the ComplianceCheckResult objects
        also now use the label `compliance.openshift.io/check-has-value`
        that lists which variables the check can use.
      - Enable Creation of TailoredProfiles without extending existing
        ones - This enhancement removes the requirement to extend an
        existing Profile in order to create a tailored Profile. The
        `extends` field from the field from the TailoredProfile CRD is
        no longer mandatory. The user can now select a list of Rule objects
        to crate a Tailored Profile from scratch. Note that you need to
        set if the Profile is meant for Nodes or Platform. You can either
        do that by setting the `compliance.openshift.io/product-type:` annotation
        or by setting the `-node` suffix for the TailoredProfile CR.
      - Make default scanTolerations more tolerant - The Compliance Operator
        now tolerates all taints, making it possible to schedule scans on
        all nodes. Previously, only master node taints were tolerated.
 - bug fixes
      - compliancescan: Fill the element and the `urn:xccdf:fact:identifier`
        for node checks - The scan results as in the ARF format now include
        the host name of the system being scanned in the `<target>`
        XML element as well as the Kubernetes Node name in the `<fact>`
        element under the `id=urn:xccdf:fact:identifier` attribute. This
        helps associate ARF results with the systems being scanned.
      - Restart profileparser on failures - In case of any failure when
        parsing a profile, we would skip annotating the object with a
        temporary annotation that prevents the object from being garbage
        collected after parsing is done. This would have manifested as
        Rules or Variables objects being removed during an upgrade.
        RHBZ: 1988259
      - Disallow empty titles and descriptions for tailored profiles - the
        XCCDF standard discourages empty titles and descriptions, so the
        Compliance Operator now requires them to be set in the TailoredProfile
        CRs
      - Remove tailorprofile variable selection check - Previously, all
        variables were only allowed to be set to a value from a selection
        set in the compliance content. This restriction is now removed, allowing
        for any values to be used.
 - internal changes:
      - Remove dead code
      - Don't shadow an import with a variable name
      - Skip e2e TestNodeSchedulingErrorFailsTheScan for now
      - e2e: Migrate TestScanProducesRemediations to use ScanSettingBinding
      - Associate variable with compliance check result

## [0.1.39] - 2021-08-23
### Changes
 - enhancements
     - Allow profileparser to parse PCI-DSS references - The Compliance
       Operator needs to be able to parse PCI-DSS references in order
       to parse compliance content that ships PCI-DSS profiles
     - Add permission for operator to remediate prometheusrule objects -
       the AU-5 control in the Moderate Compliance Profile requires the
       Compliance Operator to check for Prometheus rules, therefore the
       operator must be able to read prometheusrules.monitoring.coreos.com
       objects, otherwise it wouldn't be able to execute the rules covering
       the AU-5 control in the moderate profile
 - internal changes:
     - Print Compliance Operator version on startup
     - Update wording in TailoredProfile CRD

## [0.1.38] - 2021-08-11
### Changes
- e2e: aggregating/NA metric value
- Bug 1990836: Move metrics service creation back into operator startup
- Add fetch-git-tags make target
- Add a must-gather plugin

## [0.1.37] - 2021-08-04
### Changes
- Bug 1946512: Use latest for CSV documentation link
- doc: note that rolling back images in ProfileBundle is not well supported
- Controller metrics e2e testing
- Add initial controller metrics support
- vendor deps
- Bump the suitererunner resource limits
- Fix instructions on building VMs
- Add NERC-CIP reference support
- The remediation templating design doc Squashed
- Add implementation of enforcement remediations
- tailoring: Update the tailoring CM on changes
- Move Compliance Operator to use ubi-micro

## [0.1.36] - 2021-06-28
### Changes
- Issue warning if filter issues more than one object
- This checks for the empty remediation yaml file before creating a remediation
- Enable filtering using `jq` syntax
- Wrap warning fetching with struct
- Persist resources as JSON and not YAML
- Bug 1975358: Refresh pool reference before trying to unpause it
- TailoredProfiles: When transitioning to Ready, remove previous error message
- docs: Add an example of setting a variable in a tailoredProfile

## [0.1.35] - 2021-06-09
### Changes
- Collect all ocp-api-endpoint elements
- RBAC: Add permissions to update oauths config

## [0.1.34] - 2021-06-02
### Changes
- Switch to using go 1.16
- Remove unused const definitions
- Update dependencies
- RBAC: Allow api-resource-collector to list FIO objects

## [0.1.33] - 2021-05-24
### Changes
- Allow api-resource-collector to read PrometheusRules
- Allow api-resource-collector to read oauthclients
- Add CHANGELOG.md and make release update target
- Add permission to get fileintegrity objects
- Update go.uber.org/zap dependency
- Add permission to api-resource-collector to read MCs
- Convert XML from CaC content to markdown in the k8s objects
- Allow the api-resource collector to read ComplianceSuite objects
- Die xmldom! Die!
- Set the operators.openshift.io/infrastructure-features:proxy-aware annotation
- Make use of the HTTPS_PROXY environment variable

## [0.1.32] - 2021-04-26
### Changes
- Add Workload management annotations
- Make use of the HTTPS_PROXY environment variable
- Enhance TailoredProfile validation
- Move relevant workloads to be scheduled on the master nodes only
- Updated dependencies
- Limit resource usage for all workloads
- Updated gosec to v2.7.0
