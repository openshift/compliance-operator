# Enforcement Remediations

This describes the implementation of remediations whose purpose is not to
change a configuration in the cluster, but instead to ensure that the cluster
stays in a compliant state.

The mechanism relies on the following:

## XCCDF fixes may contain several objects

YAML manifests are able to express several manifests in one file.

We can use this capability to express several objects in one
`fix` entry. This way, when parsing the content, the `availableFixes`
in the Rule object will contain several objects if applicable.
This was already possible since we introduced an array from the
beginning in the `availableFixes` key.

The aggregator may create then several `ComplianceRemediation` objects.

To keep backwards compatibility, the first remediation object
will have the same name as the `ComplianceCheckResult` that it belongs to. Further objects will have an indexed suffix as they appear in the manifest.

## Not all remediations are mandatory

Normally, when you apply a remediation you want the object to reach
a certain state in order for the OpenSCAP rule to evaluate to `PASS`.

With enforcement remediations, this is not the case, as it's instead
an object that will ensure that the actual target object doesn't
go out of compliance. On the other hand, not all clusters might have
a policy enforcement engine, available.

So, to ensure that these enforcement remediations don't raise alarms,
objects in ComplianceAsCode can be marked with an annotation that
indicates that the remediation is optional.

In ComplianceAsCode, the annotation looks as follows:

```
    complianceascode.io/optional: ""
```

This will be translated in the operator to:

```
    compliance.openshift.io/optional: ""
```

## Remediations might depend on other Kubernetes objects

In case of remediations expressing enforcement via objects that
OPA Gatekeeper provides, there are cases where objects depend on
other objects to exist first.

This is the case, for instance, with `ConstraintTemplate` objects
which are expected to create a custom CRD. The custom CRD will thus
fail until the `ConstraintTemplate` is applied and creates the
needed object type.

These types of dependencies can now be expressed via an annotation.

In ComplianceAsCode it looks as follows:

```
    complianceascode.io/depends-on-obj: '[{"apigroup": "templates.gatekeeper.sh/v1beta1", "kind": "ConstraintTemplate", "name": "etcdencryptedonly"}]'
```

This will be translated in the Compliance Operator to the following:

```
    compliance.openshift.io/depends-on-obj: '[{"apigroup": "templates.gatekeeper.sh/v1beta1", "kind": "ConstraintTemplate", "name": "etcdencryptedonly"}]'
```

The mechanism will follow the same pattern as the XCCDF dependencies
in the remediation controller:

* The reconcile loop will verify if there are unmet dependencies
* If there are unmet dependencies, it annotates the remediation object
  indicating that there are unmet dependencies.
* If there aren't unmet dependencies, it annotates the object indicating
  that the dependencies are met.


Given that the annotation contains all the relevant information
to fetch the object, we can then just rely on `GET` calls from
the dynamic client.

If a Kubernetes dependency is missing, the controller will keep retrying
until it succeeds.

## Remediations are typed

This introduces a `type` field to the `ComplianceRemediation` spec. While this field is currently un-used,
it's meant to indicate that a remediation is meant to enforce
compliance.

The types are:

* `Configuration`: This is the default. And is meant to indicate that
  a remediation will change a configuration in the cluster.

* `Enforcement`: Is meant to indicate that a remediation enforces 
  compliance.

## There might be different enforcement engines or you might not want them at all

There are several policy enforcement engines out there, and it would be
very limiting to pick one and rely solely on that one. On the other hand,
some administrators might not want to make use of this feature at all.

To address this, we introduced the concept of "enforcement types", which is another way
to say that administrators are able to pick what enforcement remediations
they want to apply on their cluster.

This will come as an annotation in the content with the following pattern:

```
    complianceascode.io/enforcement-type: "$TYPE"
```

This will get translated to the remediation in the compliance operator
as:

```
    compliance.openshift.io/enforcement-type: "$TYPE"
```

When set, this allows us to know what engine is required to run a certain
enforcement remediation. One is able to pick the engine using the
`remediationEnforcement` key from the `ScanSettings` object.

The aforementioned `remediationEnforcement` key is also able to set
the enforcement capability off. This is done by leaving the field empty
or by explicitly setting it to `off`. This allows older deployments to
keep working with the ScanSettings that already existed, while allowing for
newer deployments to have an explicit setting.

One is also able to specify a specific engine by mentioning the engine's name.
e.g. for Gatekeeper OPA, one can specify "gatekeeper" in the
`remediationEnforcement` key.

Finally, one is able to allow all enforcement objects to get created by
setting the `remediationEnforcement` key to `all`. This is not as problematic
since deployments might not be setting the auto-apply capability on.


## Final notes

Note that this currently depends on the Gatekeeper Operator being
installed on the cluster. However, this mechanism really isn't bound
to any policy enforcement engine. It all comes from the content; so
if content is created for non-Gatekeeper enforcement policies, they could
be supported.