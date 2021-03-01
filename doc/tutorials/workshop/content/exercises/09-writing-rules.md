---
Title: Troubleshooting
PrevPage: 08-creating-profile-bundles
NextPage: ../finish
---
Writing your own Rules
======================

While the [ComplianceAsCode/content](https://github.com/ComplianceAsCode/content)
project comes with a bunch of handy rules that you can already use to test your
deployment, it is quite possible that you'll be missing a rule for your specific
benchmark, or simply you have a custom scenario that you want to address.

There is already documentation in place that will help you [write content
and understand the structure of the project](
https://github.com/ComplianceAsCode/content/blob/master/docs/manual/developer_guide.adoc#creating-content)
so let's focus instead on writing content for OpenShift.

There is a handy tool in the `utils` directory that will help you create such
rules and test them locally or against an existing cluster: [`
./utils/add_platform_rule.py`](
https://github.com/ComplianceAsCode/content/blob/master/utils/add_platform_rule.py).

Let's take it into use!

## Scenario

Let's say we want to write a rule to check if a `ConfigMap` called `my-compliant-configmap`
exists. It'll exist in the `openshift` namespace as that's where the cluster
configurations exist.

The `ConfigMap` will simply have a key called `compliant` with a value `yep`.

Let's create it:

```
$ cat << EOF | oc create -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-compliance-configmap
  namespace: openshift
data:
  compliant: yep
EOF
```

## Writing a rule

Let's create a rule that simply checks that the `ConfigMap` exists with the appropriate
value.

```
$ ./utils/add_platform_rule.py create \
    --rule must_have_compliant_cm \
    --name my-compliance-configmap --namespace openshift --type configmap \
    --title "Must have compliant CM" \
    --description "The deployment must have a CM that's compliant with.... life!" \
    --match-entity "at least one" \
    --yamlpath '.data.compliant' \
    --match "yep"
```

Where:

* `--rule` contains the name of the rule. This will also be the name of the directory where the rule is.
* `--name` contains the name of the Kubernetes object to check for.
* `--namespace` contains the namespace of the Kubernetes object to check for.
* `--type` contains the type of the Kubernetes object to check for.
* `--match-entity` defines the behavior of the check. In this case, we need at least one object with this value.
* `--yamlpath` is the path within the object that we'll search in.
* `--match` is the value that we're looking for

Note that the `--yamlpath` parameter uses the following specification: https://github.com/OpenSCAP/yaml-filter

`--match-entity` has several modes, but the most notable ones are:
* `all`: all instances found must match.
* `at least one`: As the name suggest, the check will pass if at least one instance matches.
* `none exist`: If an instance matches the check will fail.

By default, the rule will get created in the `applications/openshift` directory. e.g.
`applications/openshift/must_have_compliant_cm/rule.yml`.

The magic happens as part of the build system and on the template used by that file. Let's examine it:

```
$ cat applications/openshift/must_have_compliant_cm/rule.yml
...

template:
    name: yamlfile_value
    vars:
        filepath: /api/v1/namespaces/openshift/configmaps/my-compliance-configmap
        yamlpath: ".data.compliant"
        ocp_data: "true"
        entity_check: "at least one"
        values:
            - value: "yep"
```

This template will make the build system generate the appropriate OVAL check
that OpenSCAP will take into use. You can read more about it in the [documentation
](https://github.com/ComplianceAsCode/content/blob/master/docs/manual/developer_guide.adoc#732-list-of-available-templates)

## Testing our rule

Now that we have a rule created, we can test it on our cluster.

Before testing, make sure that you're in the `compliance-operator` namespace:

```
$ oc project openshift-compliance
```

We can test the rule as follows:

```
$ ./utils/add_platform_rule.py cluster-test --rule must_have_compliant_cm
```

This command will:

* Build the content
* Push the content to an image in your OpenShift cluster
* Create a scan that just issues that one rule 
* Get the results from that scan.

Once the command is done, you should see an output like:

```
* The result is 'COMPLIANT'
```

## Extending our rule

Let's say that we want to test for a regex instead of an exact match, so we would be able
accept values such as `yessss` or `yeah`. In this case, we'll need to adjust our command a
little:

```
$ ./utils/add_platform_rule.py create \
    --rule must_have_compliant_cm \
    --name my-compliance-configmap --namespace openshift --type configmap \
    --title "Must have compliant CM" \
    --description "The deployment must have a CM that's compliant with.... life!" \
    --match-entity "at least one" \
    --yamlpath '.data.compliant' \
    --match "ye.*" \
    --regex
```

You'll note that `--match` is now a regex, and we added the `--regex` flag to the command.
This will get reflected in the actual rule file by adding the `operation: "pattern match"`
parameter to the yaml check template:

```
$ grep operation applications/openshift/must_have_compliant_cm/rule.yml
        operation: "pattern match"
```

Let's test it out and see that the pattern still matches:

```
$ ./utils/add_platform_rule.py cluster-test --rule must_have_compliant_cm
...
* The result is 'COMPLIANT'
```

You should see a compliant result at the end of the run.

With this rule in place, we can modify the `ConfigMap` and verify that the regex indeed
matches other patterns:

```
$ oc patch -n openshift configmap my-compliance-configmap \
   -p '{"data": {"compliant": "yesss"}}' --type=merge
```

And let's verify that it still matches:

```
$ ./utils/add_platform_rule.py cluster-test --rule must_have_compliant_cm
...
* The result is 'COMPLIANT'
```

For completeness, lets modify the `ConfigMap` to have a non-compliant value and run the test:

```
$ oc patch -n openshift configmap my-compliance-configmap \
   -p '{"data": {"compliant": "hehehe nope"}}' --type=merge
$ ./utils/add_platform_rule.py cluster-test --rule must_have_compliant_cm
...
* The result is 'NON-COMPLIANT'
```

Once you've tested your rule and feel its in a good shape, you should fill in the missing
parameters from the template so they'll appear nicely in the report. Finally, you can add
the rule to a relevant profile in the `ocp4/profiles/` directory, build it, upload it to a
`ProfileBundle` and take it into use as part of your regular compliance scans!