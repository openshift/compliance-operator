# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic
Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.45] - 2021-10-28
### Changes
- Add a more verbose Changelog for the recent versions
- Add "infrastructure" to resources we always fetch
- Remove permissions for aggregator to list nodes
- Fix Kubernetes version dependency parsing bug
- Implement version applicability for remediations
- Add permissions to get and list machineset in preparation for implementation of req 3.4.1 pcidss
- Add support for rendering variable in rule objects

## [0.1.44] - 2021-10-20
### Changes
 - enhancements
     - Add option to make scan scheduling strict/not strict - adds a new
       ComplianceScan/Suite/ScanSetting option called strictNode scan.
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
