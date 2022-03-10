# Installation Guide

[Operators](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
extend Kubernetes functionality and must be installed after the cluster is
deployed.

This guide provides multiple ways to install the compliance-operator in an
OpenShift or Kubernetes cluster and assumes you have adminsitrative privileges
on the cluster to create the necessary resources.

By default, the compliance-operator is installed in the `openshift-compliance`
namespace. This guide assumes all required resources are also installed within
that namespace. You may run the operator in another namespace, but you need to
ensure all subsequent resources, like service accounts, operator groups, and
subscriptions also use the same namespace as the operator.

## Deploying packages

Deploying from package deploys the latest released version.

First, create the `CatalogSource` and optionally verify it's been created
successfuly:

```
$ oc create -f deploy/olm-catalog/catalog-source.yaml
$ oc get catalogsource -n openshift-marketplace
```

Next, create the target namespace and either install the operator from the Web
Console or from the CLI following these steps:

```
$ oc create -f deploy/ns.yaml
$ oc create -f deploy/olm-catalog/operator-group.yaml
$ oc create -f deploy/olm-catalog/subscription.yaml
```

The Subscription file can be edited to optionally deploy a custom version,
see the `startingCSV` attribute in the `deploy/olm-catalog/subscription.yaml`
file.

Verify that the expected objects have been created:

```
$ oc get sub --namespace openshift-compliance
$ oc get ip --namespace openshift-compliance
$ oc get csv --namespace openshift-compliance
```


## Deploying with Helm

The repository contains a [Helm](https://helm.sh/) chart that deploys the
compliance-operator. This chart is currently not published to any official
registries and requires that you [install](https://helm.sh/docs/intro/install/)
Helm version v3.0.0 or greater. You're required to run the chart from this
repository.

Make sure you create the namespace prior to running `helm install`:

```
$ kubectl create -f deploy/ns.yaml
```

Next, deploy a release of the compliance-operator using `helm install` from
`deploy/compliance-operator-chart/`:

```
$ cd deploy/compliance-operator-chart
$ helm install --namespace openshift-compliance --generate-name .
```

The chart defines defaults values in `values.yaml`. You can override these
values in a specific file or supply them to helm using `--set`. For example,
you can run the compliance-operator on EKS using the EKS-specific overrides in
`eks-values.yaml`:

```
$ helm install . --namespace openshift-compliance --generate-name -f eks-values.yaml
```

You can use Helm to uninstall, or delete a release, but Helm does not cleanup
[custom resource
definitions](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/#helm).
You must do this manually if you want to remove the custom resource definitions
required by the compliance-operator.

## Deploying from source

You can deploy the compliance-operator by creating the necessary resources
manually.

Make sure you create the namespace:

```
$ oc create -f deploy/ns.yaml
$ oc project openshift-compliance
```

Next, create all the necessary custom resource defintions:

```
$ for f in $(ls -1 deploy/crds/*crd.yaml); do oc apply -f $f --namespace openshift-compliance; done
```

Finally, configure the service account, permissions, and deployment:

```
$ oc apply --namespace openshift-compliance -f deploy/
```

## Verifying the installation

You can verify the operator deployed successfully by inspecting the cluster service version:

```
$ kubectl get csv --namespace openshift-compliance
```

You should also see a running deployment and pods within the namespace you
created prior to the installation:

```
$ oc get deploy --namespace openshift-compliance
$ oc get pods --namespace openshift-compliance
```

## Namespace removal

Many custom resources deployed with the compliance operators use finalizers
to handle dependencies between objects. If the whole operator namespace gets
deleted (e.g., with `oc delete ns openshift-compliance`), the order of deleting
objects in the namespace is not guaranteed. What can happen is that the
operator itself is removed before the finalizers are processed which would
manifest as the namespace being stuck in the `Terminating` state.

It is recommended to remove all custom resources and custom resource defintions
prior to removing the namespace to avoid this issue. The `Makefile` provides a
`tear-down` target that does exactly that.

If the namespace is stuck, you can work around by the issue by hand-editing or
patching any custom resources and removing the `finalizers` attributes
manually.
