# PriorityClass proposed flow design

This page describes the design and implementation of the PriorityClass
support in the compliance operator.

## Goals
We want to accomplish:
   * User can set PriorityClass in ScanSetting
   * Pods will be launched with the PriorityClass set in ScanSetting

## Add PriorityClass option to ScanSetting CRD

Administrators can choose which PriorityClass in the ScanSetting.
A sample looks as follows:

```yaml
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: my-companys-constraints
autoApplyRemediations: false
autoUpdateRemediations: false
schedule: "0 1 * * *"
priorityClass: my-priority-class
rawResultStorage:
  size: "2Gi"
  rotation: 10
# For each role, a separate scan will be created pointing
# to a node-role specified in roles
roles:
  - worker
  - master
```

We will be adding priorityClass to ScanSetting CRD as an optional field,
and if none is specified, we will treat as it didn't have a PriorityClass.
Otherwise, we will use the PriorityClass specified in the ScanSetting.

## Launch related scanning pod with set PriorityClass in ScanSetting

Once an admin set a PriorityClass in ScanSetting, when this ScanSetting
is used, we will try to launch all the pod with that PriorityClass.

## What happens if the PriorityClass is removed from ScanSetting

If the PriorityClass is removed from ScanSetting, and that ScanSetting
has not been used, nothing will be affected. 

If the PriorityClass is removed, and a scan suite has already
been launched with that ScanSetting, PriorityClass setting should be
removed starting the next scheduled scan.

