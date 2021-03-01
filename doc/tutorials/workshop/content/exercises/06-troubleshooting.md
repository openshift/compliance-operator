---
Title: Troubleshooting
PrevPage: 05-tailoring-profiles
NextPage: 07-content-setup
---
General tips
------------

* The Compliance Operator emits Kubernetes events when something
  important happens. You can either view all events in the cluster (`oc get events
  -nopenshift-compliance`) or events for an object, e.g. for a scan
  (`oc describe compliancescan/$scan`)

* The Compliance Operator consists of several controllers, roughly
  one per API object. It could be handy to filter only those controller that correspond to
  the API object having issues, e.g. if a `ComplianceRemediation` can't be applied,
  the first place to look might be the messages from the `remediationctrl` controller.
  You can filter the messages from a single controller e.g. using `jq`:
  `oc logs -l name=compliance-operator | jq -c 'select(.logger == "profilebundlectrl")' `

* The timestamps are logged as seconds since UNIX epoch in UTC. To convert
  them to a human-readable date, use
  `date -d @timestamp --utc`, e.g. `date -d @1596184628.955853 --utc`

* Many CRs, most importantly `ComplianceSuite` and `ScanSetting` allow
  the `debug` option to be set. Enabling this option increases verbosity
  of the openscap scanner pods as well as some other helper pods.

Useful labels
-------------

Each pod that's spawned by the compliance-operator is labeled specifically with
the scan it belongs to and the work it does.

The scan identifier is labeled with the `compliance.openshift.io/scan-name`
label.

The workload identifier is labeled with the `workload` label.

The compliance-operator schedules the following workloads:

* **scanner**: Performs the actual compliance scan.

* **resultserver**: Stores the raw results for the compliance scan.

* **aggregator**: Aggregates the results, detects inconsistencies and outputs
  result objects (checkresults and remediations).

* **suitererunner**: Will tag a suite to be re-run (when a schedule is set).

* **profileparser**: Parses a datastream and creates the appropriate profiles,
  rules and variables.

So, when debugging and needing the logs for a certain workload, it's possible
to do:

```
oc logs -l workload=<workload name>
```


Troubleshooting OpenSCAP
------------------------

Specially while developing new rules and profiles, it's useful to be able to debug the deployment
and see the output of the underlying `oscap` command.

The **ScanSetting** object exposes a `debug` flag that allows you to see more verbose logs.
Let's set it:

```
$ oc patch scansettings periodic-setting -p '{"debug":true}' --type=merge
scansetting.compliance.openshift.io/periodic-setting patched
```

This setting will be taken into use once the scans re-run. To force a re-run, let's
annotate one of the scans:

```
$ oc annotate compliancescan rhcos4-no-kptr-restrict-worker "compliance.openshift.io/rescan="
compliancescan.compliance.openshift.io/rhcos4-no-kptr-restrict-worker annotated
```

you'll notice that the scan is now running:

```
$ oc get compliancescans rhcos4-no-kptr-restrict-worker
NAME               PHASE     RESULT
rhcos4-no-kptr-restrict-worker   RUNNING   NOT-AVAILABLE
```

> **NOTE**
> 
> Using the [`oc-compliance`](https://github.com/JAORMX/oc-compliance) plugin 
> it's also possible to re-run the scans using the subcommand
> `oc compliance rerun-now`. For this example, the invocation would have been:
> 
> ```
> $ oc compliance rerun-now compliancescan rhcos4-no-kptr-restrict-worker
> ```
> This will annotate the scan and re-run it.
> 
> The advante of this subcommand is that it'll also work on `ComplianceSuite` and
> `ScanSettingBinding` objects. Which means that it'll re-trigger all of the scans
> created by these objects.
>
> ```
> $ oc compliance rerun-now -h
> ```

To get all the pods for this scan, we can do:

```
$ oc get pods -l compliance.openshift.io/scan-name=rhcos4-no-kptr-restrict-worker
NAME                                                              READY   STATUS      RESTARTS   AGE
aggregator-pod-rhcos4-no-kptr-restrict-worker                     0/1     Completed   0          60s
rhcos4-no-kptr-restrict-worker-ip-10-0-143-189.ec2.internal-pod   0/2     Completed   0          100s
rhcos4-no-kptr-restrict-worker-ip-10-0-156-174.ec2.internal-pod   0/2     Completed   0          100s
rhcos4-no-kptr-restrict-worker-ip-10-0-174-227.ec2.internal-pod   0/2     Completed   0          100s
```

To get only pods for a specific workload (e.g. only scanner pods), we have the `workload`
label available:

```
$ oc get pods -l compliance.openshift.io/scan-name=rhcos4-no-kptr-restrict-worker,workload=scanner
NAME                                                              READY   STATUS      RESTARTS   AGE
rhcos4-no-kptr-restrict-worker-ip-10-0-143-189.ec2.internal-pod   0/2     Completed   0          2m48s
rhcos4-no-kptr-restrict-worker-ip-10-0-156-174.ec2.internal-pod   0/2     Completed   0          2m48s
rhcos4-no-kptr-restrict-worker-ip-10-0-174-227.ec2.internal-pod   0/2     Completed   0          2m48s
```

To see the `oscap` logs, which are now verbose, we can fetch the logs of one of these pods,
and specifically for the `scanner` container:

```
$ oc logs rhcos4-no-kptr-restrict-worker-ip-10-0-143-189.ec2.internal-pod -c scanner
Running oscap-chroot as oscap-chroot /host xccdf eval --verbose INFO --fetch-remote-resources --profile xccdf_org.ssgproject.content_profile_e8 --results-arf /tmp/report-arf.xml /content/ssg-rhcos4-ds.xml
The scanner returned 2
I: oscap: Identified document type: data-stream-collection
I: oscap: Created a new XCCDF session from a SCAP Source Datastream '/content/ssg-rhcos4-ds.xml'.
I: oscap: Identified document type: Benchmark
I: oscap: Identified document type: cpe-list
I: oscap: Started new OVAL agent ssg-rhcos4-oval.xml.
I: oscap: Querying system information.
I: oscap: Starting probe on URI 'queue://system_info'.
I: oscap: Switching probe to PROBE_OFFLINE_OWN mode.
...
```

You can also get all the logs for the scanners at the same time:

```
$ oc logs -l compliance.openshift.io/scan-name=rhcos4-no-kptr-restrict-worker,workload=scanner -c scanner --tail=-1  > oscap-logs.txt
```

Be aware that this is very verbose output, so you might want to store this in a file for further
inspection.

***

In the next section we'll [prepare our development environment to start
writing content](07-content-setup.md).
