# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic
Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.40] - 2021-09-09
### Changes
- Remove tailorprofile variable selection check
- Disallow empty titles and descriptions for tailored profiles
- Restart profileparser on failures
- Skip e2e TestNodeSchedulingErrorFailsTheScan for now
- Make default scanTolerations more tolerant
- e2e: Migrate TestScanProducesRemediations to use ScanSettingBinding
- Associate variable with compliance check result
- Enable Creation of TailoredProfiles without extending existing ones
- Don't shadow an import with a variable name
- compliancescan: Fill the <target> element and the urn:xccdf:fact:identifier for node checks
- Remove dead code
- Add supoort for remediation templating for operator

## [0.1.39] - 2021-08-23
### Changes
- Allow profileparser to parse PCI-DSS references
- Add permission for operator to remediate prometheusrule objects
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
