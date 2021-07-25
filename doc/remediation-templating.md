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

## High-level overview

The compliance operator allows an administrator to extend a pre-existing profile, 
to enable, disable rules and set values that come from the ProfileBundle. When 
this happens, a CR `TailoredProfile` will be generated. Currently the `ComplianceScan`
will use the tailored rules and variables from `TailoredProfile` CR, however,
the `ComplianceRemediation` will now take custom values from `TailoredProfile`.
Here we are going to implement that feature.

### ComplianceAsCode remediation rule format


In ComplianceAsCode, when the SCAP remediation content is generated, normally we will 
have fixed remediation. In this proposal, we are going to change the remediation rule
formate for the Kubernetes to include a marked reference variable to reference related
XCCDF values from rules. 

The following example is parts of two YAML files from ComplianceAsCode content, 
the first one is the rule, and the second is the remediation:
```yaml
documentation_complete: true
title: 'Set SSH Idle Timeout Interval'
...
requires:
    - sshd_set_keepalive

ocil_clause: 'it is commented out or not configured properly'

ocil: |-
    Run the following command to see what the timeout interval is:
    <pre>$ sudo grep ClientAliveInterval /etc/ssh/sshd_config</pre>
    If properly configured, the output should be:
    <pre>ClientAliveInterval {{{ xccdf_value("sshd_idle_timeout_value") }}}</pre>
...
```
As showing in the rule YAML file above, an XCCDF value for SSH Idle Timeout Interval is set.

In the remediation YAML file, we can modify that Jinja Macro to pass the name of the 
XCCDF variable and its value `sshd_idle_timeout_value`, and then the Macro will substitute the related 
`ClientAliveInterval` values to `VAR_SSHD_IDLE_TIMEOUT_VALUE` as defined in the ocil:
```yaml
---
# platform = multi_platform_ocp,multi_platform_rhcos
# reboot = false
# strategy = restrict
# complexity = low
# disruption = low
{{{ kubernetes_sshd_set("ClientAliveInterval","sshd_idle_timeout_value") }}}
```

And the machine config object data will be something like below before it gets url-encoded.
```jinja
...
PrintLastLog yes
#TCPKeepAlive yes
PermitUserEnvironment no
Compression no
ClientAliveInterval 600
ClientAliveCountMax VAR_SSHD_IDLE_TIMEOUT_VALUE

#UseDNS no
#PidFile /var/run/sshd.pid
#MaxStartups 10:30:100
#PermitTunnel no
...
```
Since the url encode doesn't encode ASCII alpha-numerical nor underscore characters, and
we can later substitute the place holder after the url encoding from `VAR_SSHD_IDLE_TIMEOUT_VALUE`
to `{{.sshd_idle_timeout_value | urlquery}}`, so that it can be post-process them with the appropriate
escaped instances in the compliance operator. The following code is the example of final result:
```xml
<fix rule="sshd_set_idle_timeout" complexity="low" disruption="low" reboot="false" strategy="restrict">---
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
spec:
  config:
    ignition:
      version: 3.1.0
    storage:
      files:
      - contents:
          source: data:, `PrintLastLog%20yes%0A%23TCPKeepAlive%20yes%0APermitUserEnvironment%20no%0ACompression
          %20no%0AClientAliveInterval%20{{.sshd_idle_timeout_value|urlquery}}%0AClientAliveCountMax%200%0A%0A%23UseDNS%2
          0no%0A%23PidFile%20%2Fvar%2Frun%2Fsshd.pid%0A%23MaxStartups%2010%3A30%3A100%0A%23PermitTunnel%20no`
        mode: 0600
        path: /etc/ssh/sshd_config
        overwrite: true
</fix>
```

We make some changes into the Parsing Utility since we brought a marker XCCDF variable into
the remediation content, we need to substitute all the placeholder with corresponding values
before we create the `ComplianceRemediation` object. We need to fetch all the variables that 
are in the scan result `ConfigMap` for all the nodes with consistency check(Note that all the 
tailored set values are parsed to the resulting CM), and then since we have XCCDF values marked 
using `{{}}` in the remediation rules there is a Go Library called `Template` that we can 
use to match, url-encode, and substitute the value with the XCCDF variable set in the Tailored Profile 
from the result `ConfigMap` and then to create the `ComplianceRemediation` object with 
corresponding values.

Proof of concept using golang templating https://golang.org/pkg/text/template/#hdr-Functions 
```go
var value_dic = map[string]string{
	"the_value_you_want_to_define":  "3600",
	"the_value_you_want_to_define2": "1111",
	"the_value_you_want_to_define3": "2222",
	"the_value_you_want_to_define4": "3333",
}
var xccdf_variable = []string{} // list of variable was used to render the MachineConfig
var missing_value = []string{} // list contain all missing value variables

func findXCCDFValue(format string) string {
	value, ok := value_dic[format]
	if (ok && value != "xccdf-value-require-input") {
    xccdf_variable = append(xccdf_variable, format) //append name of xccdf value used
		return value
	} else {
    missing_value = append(missing_value, format) //append missing value variable name to the missing value list
		return "Value-Not-Set"
	}

}

func main() {
	MachineConfigCM := "data:,...%0APrintMotd%20no%0A%0APrintLastLog%20yes%0A%23TCPKeepAlive%20yes%0APermitUserEnvironment%20no%0ACompression%20no%0AClientAliveInterval%20{{\"the_value_you_want_to_define65\" | findXCCDFValue | urlquery}}%0AClientAliveCountMax%200%0A%23UseDNS%20no%0A%23PidFile%20/var/run/sshd.pid%0A%23MaxStartups%2010%3A30%3A100%0A%23PermitTunnel%20no%0A%23ChrootDirectory%20none%0A%23..."
	t, err := template.New("text").Funcs(template.FuncMap{"findXCCDFValue": findXCCDFValue}).
		Parse(MachineConfigCM)
	if err != nil {
		panic(err)
	}
	t.Execute(os.Stdout, "nothinghere")

  if(len(xccdf_variable)>0){
    labels_var_set[compv1alpha1.RemediationVariableUsedLabel] = missing_value
    remediation[compv1alpha1.RemediationXCCDFVariableUsedAnnotation] //from here we will save all the set xccdf variables
    rem.SetLabels(labels)
  }

  if(len(missing_value) > 0){
    labels[compv1alpha1.RemediationHasUnsetVariableLabel] = missing_value
    remediation[compv1alpha1.RemediationHasUnsetVariableAnnotation] //from here we will save the not-set variables in the label
    rem.SetLabels(labels)
  }
}
```


In the case, where an administrator has to set a value for the remediation but did not set one.
In the aggregator, before we create the Remediation, if value not set or no default value found,
we should add a label `compliance.openshift.io/has-unset-variable`, and `compliance.openshift.io/has-unset-xccdf-values` 
annotation with the missing value variables. We should also have a label `compliance.openshift.io/variable-used` and a annotation
`compliance.openshift.io/xccdf-variable-used`, And in the remediation controller, we should check the label,
if label `compliance.openshift.io/has-unset-xccdf-variable` not empty, we should change
`ComplianceRemediation` status to `Needs-Review`, and prevent it from applying.

Notes: The content creator can use `xccdf-value-require-input` as a default value for some variable in the rule so that it will require a tailored profile to use that rule and must have a set value for that variable as well. 

### Proposed user-flow with examples

#### Administrator user-flow
Run the operator first, and then assume that an administrator wants
to do a scan based on nist-moderate with a custom `sshd-idle-timeout-value` set to 
`3600` instead of `300` as the default value. We will create a `TailoredProfile`:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: TailoredProfile
metadata:
  name: nist-moderate-modified
spec:
  extends: rhcos4-moderate
  title: My modified nist profile with a custom value
  setValues:
  - name: rhcos4-sshd-idle-timeout-value
    rationale: test for a custom value
    value: '3600'
```

Run`oc create -n <namespace> -f <file-name>.yaml`, a CR ConfigMap
`nist-moderate-modified-tp` will gets created. Next, he will create a ScanBinding
and run the scan:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: nist-moderate-modified
profiles:
  - apiGroup: compliance.openshift.io/v1alpha1
    kind: Profile
    name: ocp4-moderate
  - apiGroup: compliance.openshift.io/v1alpha1
    kind: TailoredProfile
    name: nist-moderate-modified
settingsRef:
  apiGroup: compliance.openshift.io/v1alpha1
  kind: ScanSetting
  name: default
  ```

The scan will use the values set from the `TailoredProfile`, and the aggreator
will use the parsing utility to fetch the values from Result `ConfigMap`, to 
replace the `{{sshd-idle-timeout-value}}` marker to the url encoded of `3600` 
that set in the TailoredProfile, the `MachineConfig` object for 
the `ComplianceRemediation` will be generated as below:

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
          source: data:, `PrintLastLog%20yes%0A%23TCPKeepAlive%20yes%0APermitUserEnvironment%20no%0ACompression
          %20no%0AClientAliveInterval%203600%0AClientAliveCountMax%200%0A%0A%23UseDNS%2
          0no%0A%23PidFile%20%2Fvar%2Frun%2Fsshd.pid%0A%23MaxStartups%2010%3A30%3A100%0A%23PermitTunnel%20no`
        mode: 0600
        path: /etc/ssh/sshd_config
        overwrite: true
```
There are some some annotation and labels we used for the compliance remediation:

`compliance.openshift.io/value-required`: we used this one for content creator to mark XCCDF variables that is rquired to have a tailored input from administrator who runs the scan. 

`compliance.openshift.io/unset-value`: We used this annotation to mark all the XCCDF variables that are successfully parsed, but are not found in `ResultConfigMap` or there the value is in `compliance.openshift.io/value-required` annotation, but the tailored value for that value was not set by the administrator.

`compliance.openshift.io/xccdf-value-used`: This one included all the values that were initially parsed as well as value set from `value-required`

The remediation status will become `NeedsReview` where if there is `unset-value`
If the the Remediation Status become `Needs-Review`, the administrator can check for the annotation `compliance.openshift.io/has-unset-xccdf-values` for that `ComplianceRemediation`,
because some rules require to have a custom value to be set, or it didn't come with a default value. The Following is a example of missing value ComplianceRemediation object look like.

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
  creationTimestamp: '2021-07-01T19:06:43Z'
  annotations: 
    compliance.openshift.io/has-unset-xccdf-values: "sshd_idle_timeout_value"
  labels:
    compliance.openshift.io/has-unset-variable: "sshd_idle_timeout_value"
```

#### Content writer user-flow

As a content writer:
It is now possible to write remediation content for Kubernetes and use the XCCDF values from
the rules file.

Continue with this rule as an example:

```yaml
documentation_complete: true
title: 'Set SSH Idle Timeout Interval'
...
requires:
    - sshd_set_keepalive

ocil_clause: 'it is commented out or not configured properly'

ocil: |-
    Run the following command to see what the timeout interval is:
    <pre>$ sudo grep ClientAliveInterval /etc/ssh/sshd_config</pre>
    If properly configured, the output should be:
    <pre>ClientAliveInterval {{{ xccdf_value("sshd_idle_timeout_value") }}}</pre>
...
```

First, the content writer can write a rule, if they want to use XCCDF Value,
they need to defind the value in the `ocil`, the formate will be like  
`{{{ xccdf_value("the_value_you_want_to_define") }}}` and use a value variable
in the rules, and then they can use the XCCDF value `the_value_you_want_to_define` in their 
remediations contents. Ideally, there is some steps that a Jinja Macro script needs to do:

1. take `the_value_you_want_to_define` in remediation content and then replace the corresponding values to `VAR_THE_VALUE_WANT_TO_DEFINE`
2. url-encode the whole thing 
3. substitue `VAR_THE_VALUE_WANT_TO_DEFINE` to `{{.the_value_you_want_to_define|urlquery}}` and generate kube remediation


After the content build, the machine config object will have the custom XCCDF value in this formate
`data:,...some-url-encoding{{.the_value_you_want_to_define|urlquery}}some-url-encoding` in kube remediation xml.

The real values sustitution will happen in the compliance operator
after the Tailor scan, go template function in the operator will do the substitution from `data:,...some-url-encoding{{.the_value_you_want_to_define|urlquery}}some-url-encoding` 
to `data:,...some-url-encoding urlencoded(SOMESETVALUES) some-url-encoding`
The post-processed `ComplianceRemediation` kube object will look like following:
```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceRemediation
metadata:
  selfLink: >-
    /apis/compliance.openshift.io/v1alpha1/namespaces/openshift-compliance/complianceremediations/nist-moderate-modified-master-sshd-set-idle-timeout
  resourceVersion: '35343'
  name: nist-moderate-modified-master-sshd-set-idle-timeout
  uid: 085f51e4-19bb-4478-9398-df409858dce6
  creationTimestamp: '2021-07-01T19:06:43Z'
  annotations: 
    compliance.openshift.io/xccdf-variable-used: sshd_idle_timeout_value
  labels:
    compliance.openshift.io/variable-used: sshd_idle_timeout_value

  
 ...
spec:
  apply: false
  current:
    object:
      apiVersion: machineconfiguration.openshift.io/v1
      kind: MachineConfig
      spec:
        config:
          ignition:
            version: 3.1.0
          storage:
            files:
              - contents:
                  source: >-
                    data:,...%0APrintMotd%20no%0A%0APrintLastLog%20yes%0A%23TCPKeepAlive%20yes%0APermitUserEnvironment%20no%0ACompression%20no%0AClientAliveInterval%203600%0AClientAliveCountMax%200%0A%23UseDNS%20no%0A%23PidFile%20/var/run/sshd.pid%0A%23MaxStartups%2010%3A30%3A100%0A%23PermitTunnel%20no%0A%23ChrootDirectory%20none%0A%23...
                mode: 384
                overwrite: true
                path: /etc/ssh/sshd_config
  outdated: {}
status:
  applicationState: NotApplied

```


## Creating content using template

Currently, for certain types of rules, there are provided templates. And you only need to specify the template name and its parameters in rule.yml and the content will be generated during the build. However, we want to add values support for the Kubernetes template.

For example, in `shared/templates/sshd_lineinfile/kubernetes.template` We have following:
```yaml
# platform = multi_platform_ocp,multi_platform_rhcos
# reboot = false
# strategy = restrict
# complexity = low
# disruption = low
{{{ kubernetes_sshd_set() }}}
```
We can change the template to `parameter=PARAMETER, value=VALUE` to be able to use custom variables.
```yaml
# platform = multi_platform_ocp,multi_platform_rhcos
# reboot = false
# strategy = restrict
# complexity = low
# disruption = low
{{{ kubernetes_sshd_set(parameter=PARAMETER, value=VALUE) }}}
```

To use a template in rule.yml add template: key there and fill it accordingly. The general form is the following:
```yaml
template:
    name: template_name
    vars:
        param_name: values_name # you can set parameter, and its values 
        value: values
    backends: # optional
        kubernetes: "on"
```
The`vars` contains template parameters and their values which will be substituted into the template. Each template has specific parameters.

Example as Set SSH Idle Timeout Interval, We can use the `ssh_lineinfile` template as:
```yaml
template:
    name: sshd_lineinfile
    vars:
        parameter: ClientAliveInterval
        value: sshd_idle_timeout_value
    backends: # optional
        kubernetes: "on"
```
Parameters:
    parameter - name of the SSH configuration option as `ClientAliveInterval`
    value - value of the SSH configuration option specified by parameter as `sshd_idle_timeout_value`

So the ideal remediation output for kubernetes will be following:
```yaml
{{{ kubernetes_sshd_set("ClientAliveInterval", "$sshd_idle_timeout_value") }}}
```
