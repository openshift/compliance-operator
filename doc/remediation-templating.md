# Remediation template proposed flow design
This page describes the design and implementation of the remediation templating
support in the compliance operator.

## Goals
We want to accomplish:
   * The remediation content can use variables that SCAP supports instead of a pre-set 
     fixed value
   * During a scan where an administrator set custom variables in the tailored profile, it
     is possible to create `ComplianceRemediation` objects based on the given custom values
   * As a content writer, it is possible to use XCCDF value from rules in Kubernetes remediation
   * Add additional remediation state that shows "Needs-Review" for not set and no default values 
     remediation
   * Value-Required annotation to require an administrator to set a value for some variables

## High-level overview

The compliance operator allows an administrator to extend a pre-existing profile, 
to enable, disable rules, and set values that come from the ProfileBundle by creating a `TailoredProfile` CR. 

## ComplianceAsCode remediation rule format (Content creating user-flow)

Compliance Operator supports various ways to use a variable in remediation for different situations:

### Single value variable in url-encoded content (ex. `var_sshd_idle_timeout_value` in MachineConfig Remediation)

There are jinja functions that can be used to generate machine config:

`kubernetes_machine_config_file(path='', file_permissions_mode='', source='')`

The content developer can make use of this jinja function to generate `MachineConfig`, remediation.

```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
spec:
  config:
    ignition:
      version: 3.1.0
    storage:
      files:
      - contents:
          source: data:,{{ {{{ url_encode(source) }}} }}
        mode: {{{ file_permissions_mode }}}
        path: {{{ path }}}
        overwrite: true
```

The `source` above contains the file content, and the xccdf variables are often inside those file content. For example, there is an rule that configures auditd max_log_file_action Upon Reaching Maximum Log Size. We want to set `max_log_file_action` to a xccdf variable in file content like the following:

```yml
...
priority_boost = 4
name_format = hostname
##name = mydomain
max_log_file_action = {{.var_auditd_max_log_file_action}}
space_left = {{.var_auditd_space_left}}
space_left_action = {{.var_auditd_space_left_action}}
verify_email = yes
...
```

To generate a remdiation for the rule, we can created `kubernets/shared.yml` file under that rule's folder, and save the content of file above to a variable to use as `source`, then call `kubernetes_machine_config_file(path='/etc/audit/auditd.conf', file_permissions_mode='0640', source=content_of_file` there.

The file content `source` needs to get URL-encoded in `MachineConfig`, and in order to process the variable substitutions in the compliance operator, it needs to get URL-encoded decoded.

We brought in a special marker for URL-encoded data to easier identify those URL-encoded file content, we added `{{ ` and ` }}` around the url_encoded content(beware that space is needed here), this marker will get recognized in Compliance Operator. 

If a xccdf variable is `variable_name`, it needs to have the following format in file content:

`{{.variable_name}}`

There is an example where `max_log_file_action` need to be set to `var_auditd_max_log_file_action`, following format is what we need to do in the file content `source`:

`max_log_file_action = {{.var_auditd_max_log_file_action}}`


### multiple values variable in url-encoded content (ex. `var_multiple_time_servers` in MachineConfig Remediation)

There will be situations where multiple values are inside one variable, and we need to iterate over the values.

ex. We can look at rule `/linux_os/guide/services/ntp/chronyd_specify_remote_server`, we need to set ntp server using
`var_multiple_time_servers`, but we can't do a simple substitution here because `var_multiple_time_servers` have multiple 
variables in it, and we need to set one server per line.

`var_multiple_time_servers` have values: `default: "0.pool.ntp.org,1.pool.ntp.org,2.pool.ntp.org,3.pool.ntp.org"`
and in file content `source` we need to set as follows:

```
server 0.pool.ntp.org minpoll 4 maxpoll {{$var_time_service_set_maxpoll}}
server 1.pool.ntp.org minpoll 4 maxpoll {{$var_time_service_set_maxpoll}}
server 2.pool.ntp.org minpoll 4 maxpoll {{$var_time_service_set_maxpoll}}
server 3.pool.ntp.org minpoll 4 maxpoll {{$var_time_service_set_maxpoll}}
```

We can use following jinja templating format to do so, and put then in source before url-encoding:
```jinja
# This file controls the configuration of the ntp server
# {{.var_multiple_time_servers}} we have to put variable array name here for mutilines remediation 
{{$var_time_service_set_maxpoll:=.var_time_service_set_maxpoll}}
{{range $element:=.var_multiple_time_servers|toArrayByComma}}server {{$element}} minpoll 4 maxpoll {{$var_time_service_set_maxpoll}}
{{end}}
```
  
### Single value variable in non-url-encoded content (ex. variable in KubeletConfig Remediation)

In order to support xccdf variable templating in a wide variety of remediation objects, you can also use xccdf variable
by just putting them where need to be in the remediation, and put `{{.` and `}}` around the variable.

For example, in rule `kubelet_configure_event_creation`, we can set `var_event_record_qps` as following format in the remediaton.
```yaml
---
# platform = multi_platform_ocp
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
spec:
  kubeletConfig:
    eventRecordQPS: {{.var_event_record_qps}}
```

### Additional features: Value-Required annotation.

We have `kubernetes_machine_config_file_with_required_value(path='', file_permissions_mode='', source='', vals=[])`
A content writer can put variable name in `val` to force an administrator to set a value for that variable before they can 
successfully apply the remediation.

It will set an annotation for machine config remediation: `complianceascode.io/value-input-required:` 

### Additional features: Use xccdf variable in rules

In the rule's description and rationale, we can use xccdf variable, and it will get rendered in its default values in CO.

Ex. you can use `var_api_min_request_timeout` as `{{{ xccdf_value("var_api_min_request_timeout") }}}` between texts in description 
and rational sections of a rule, and they will render to `var_api_min_request_timeout` default value `3600`



## Administrator user-flow

Here are some annotations and labels we used for the compliance remediation:
 
`compliance.openshift.io/value-required`: we used this one for the content creator to mark XCCDF variables to be required, and an administrator who runs the scan needs to set a value for that XCCDF variables, or else it will show as `NeedsReview` for that remediation.
 
`compliance.openshift.io/unset-value`: We used this annotation to mark all the XCCDF variables that are not found in `ResultConfigMap` or when there are values in `compliance.openshift.io/value-required` annotation, but those values are not found in the tailored profile.
 
`compliance.openshift.io/xccdf-value-used`: This one included all the values that were initially parsed as well as the value set from `compliance.openshift.io/value-required`.
 
`compliance.openshift.io/has-unset-variable`: we use this one to label remediation that has unset variables

### Run a single scan 

Run the operator first, and then assume that an administrator wants to do a scan on one rule `ocp4-audit-profile-set`, instead of using the `default` for audit log policy, they want to set the audit log policy to `WriteRequestBodies`:

Apply this yaml to create a tailored profile select the rule, and set Value,

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: TailoredProfile
metadata:
  name: ocp4-audit-profile-set-tailored
  namespace: openshift-compliance
spec:
  description: test ocp4-audit-profile-set-tailored
  title: ocp4 set tailored profile
  enableRules:
    - name: ocp4-audit-profile-set
      rationale: set audit profile
  setValues:
  - name: ocp4-var-openshift-audit-profile 
    rationale: set audit profile value
    value: 'WriteRequestBodies'
```

Now we have the tailored profile ready, then they need to set up `ScanSettingBinding` to generate the actual scan:
Here we assume they want the remediation to get auto-applied after the scan.

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: ocp4-audit-profile-set-ssb
profiles:
  - apiGroup: compliance.openshift.io/v1alpha1
    kind: TailoredProfile
    name: ocp4-audit-profile-set-tailored
settingsRef:
  apiGroup: compliance.openshift.io/v1alpha1
  kind: ScanSetting
  name: default-auto-apply
```

After the scan, if the cluster is non-compliant, the remediation will be generated as follows:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
  annotations:
    compliance.openshift.io/xccdf-value-used: var-openshift-audit-profile
  name: ocp4-audit-profile-set-tailored-audit-profile-set
  namespace: openshift-compliance
  ownerReferences:
    - apiVersion: compliance.openshift.io/v1alpha1
      blockOwnerDeletion: true
      controller: true
      kind: ComplianceCheckResult
      name: ocp4-audit-profile-set-tailored-audit-profile-set
      uid: f937c6a9-533f-4af1-a680-8f39b2b86ffd
  labels:
    compliance.openshift.io/scan-name: ocp4-audit-profile-set-tailored
    compliance.openshift.io/suite: ocp4-audit-profile-set-ssb
spec:
  apply: true
  current:
    object:
      apiVersion: config.openshift.io/v1
      kind: APIServer
      metadata:
        name: cluster
      spec:
        audit:
          profile: WriteRequestBodies
  outdated: {}
  type: Configuration
status:
  applicationState: Applied
```

As we can see from here, the audit log policy is set to `WriteRequestBodies` in this remediation. If the administrator didn't 
specify the value in the tailored profile, the audit log policy variable will be set to default instead in this remediation.

### Check for unset variables and required value
 
The remediation status will become `NeedsReview` whenever there is a `compliance.openshift.io/unset-value`

If the Remediation Status becomes `NeedsReview`, the administrator can check for the annotation `compliance.openshift.io/has-unset-xccdf-values` as well as `compliance.openshift.io/value-required` for that `ComplianceRemediation`, because some rules require to have a custom value to be set, or it didn't come with a default value. The admin needs to manually set one in the tailored profile. The following is an example of what a missing value ComplianceRemediation object looks like.
 
 
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
 creationTimestamp: '2021-07-01T19:06:43Z'
 annotations:
   compliance.openshift.io/has-unset-xccdf-values: 'sshd_idle_timeout_value'
   compliance.openshift.io/value-required: 'sshd_idle_timeout_value'
 labels:
   compliance.openshift.io/has-unset-variable: ''
```
 
The above situation can be caused by the situation where an administrator did not set a value for `sshd_idle_timeout_value` in the TailorProfile before the scan, to find which value is available to set for that variable in order to fix that remediation, an admin can use:
 
`$ oc describe variable rhcos4-sshd-idle-timeout-value -nopenshift-compliance`
 
An admin can find a section of value for variable `sshd-idle-timeout-value` to choose from, and they can set that value in a tailored profile to satisfy the `compliance.openshift.io/value-required`. Noted, an admin can also set the variable to any other value besides the section values.
