------------------
Title: Tailored profiles
PrevPage: 03-remediations
NextPage: 06-troubleshooting
------------------

Tailored Profiles
=================
While the default `Profile` objects represent a sensible baseline, each
environment is different and might require a different set of rules. This
chapter will show you how to construct a `TailoredProfile` which builds
upon a `Profile` but extends and modifies it.

Disable rules in a `Profile`
----------------------------
As an example, we're going to modify the `rhcos-e8` profile to disable
the rule `rhcos4-sysctl-kernel-kptr-restrict`. Perhaps our environment
needs that functionality or is hitting an issue with the remediation
applied.

In order to do that, we're going to create a `TailoredProfile` based
on the original `rhcos4-e8` profile by applying the following manifest:
```
$ cat << EOF > rhcos-4-no-kptr-restrict.yaml 
apiVersion: compliance.openshift.io/v1alpha1
kind: TailoredProfile
metadata:
  name: rhcos4-no-kptr-restrict
  namespace: openshift-compliance
spec:
  extends: rhcos4-e8
  title: RHCOS4 E8 profile disables the kptr-restrict rule
  disableRules:
    - name: rhcos4-sysctl-kernel-kptr-restrict
      rationale: We really need this functionality
EOF
```

Except for the usual metadata like name and namespace, there are several
attributes to note in the manifest:
 * **spec.extends** - this is the name of the `Profile` object we are
   basing the `TailoredProfile` on. It must exist.
 * **spec.title** - Human-readable description of the `TailoredProfile`
 * **spec.disableRules.name** - the name of the rule we are disabling
 * **spec.disableRules.rationale** - explanation of why we are disabling
   the rule.

Save the manifest in a file and create the Kubernetes object:
```
$ oc create -f rhcos-4-no-kptr-restrict.yaml 
tailoredprofile.compliance.openshift.io/rhcos4-no-kptr-restrict created
```

Now that we have the TailoredProfile ready, we need to modify the previously
created `ScanSettingBinding` object to use this `TailoredProfile` instead
of the `Profile` it was using previously. We can do this by deleting the
previously created `ScanSettingBinding` and creating a new one. Note that
it would also be possible to edit the `ScanSettingBinding`, but for demonstration
purposes, it is easier to recreate the object as the scans would also be
re-ran automatically. The new `ScanSettingBinding` manifest looks like this:
```
$ cat << EOF > bindings-tailored.yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: periodic-e8
  namespace: openshift-compliance
profiles:
  # Node checks
  - name: rhcos4-no-kptr-restrict
    kind: TailoredProfile
    apiGroup: compliance.openshift.io/v1alpha1
  # Cluster checks
  - name: ocp4-e8
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
settingsRef:
  name: periodic-setting
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
EOF
```
Note that the first item in the `profiles` array is now of kind
`TailoredProfile` and points to the previously created `rhcos4-no-kptr-restrict`
`TailoredProfile` object. Save this manifest and create the new binding
in place of the old one:
```
$ oc delete scansettingbindings --all
$ oc create -f bindings-tailored.yaml
scansettingbinding.compliance.openshift.io/periodic-e8 created
```
Now watch the compliance scans until they reach the `DONE` state:
```
$ oc get compliancescans -w
```
After the scan finishes, you can list the `ComplianceCheckResult` objects
and verify that the `rhcos4-no-kptr-restrict` rule is no longer being ran.

There are more ways to tailor a profile, including enabling rules that
the profile might disable by default or setting custom values for variables
the profile might expose such as the SELinux state. You can learn more
about the `TailoredProfile` by calling `oc explain tailoredprofile.spec`.

***

Let's now move to the next section will will give us tips on how to
[debug scans for the compliance-operator](06-troubleshooting.md)
