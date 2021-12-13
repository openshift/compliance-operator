# End to end test documentation

Running e2e ensures various parts of the Compliance Operator are working correctly in a real cluster environment. And we are able to test out new rules and new features using e2e test.
The tests file directories for e2e are under tests/e2e/

`tests/e2e/e2e-tests.go` - contains all the tests we have, we can add new tests to that file.

## How to add a new test

First, the structure for the test is following:

```go
	testExecution{
			Name:       "TestKubeletConfigRemediation",
			IsParallel: false,
			TestFn: func(t *testing.T, f *framework.Framework, ctx *framework.Context, mcTctx *mcTestCtx, namespace string) error {
            ...
            }
    }
```
We can set `IsParallel` to instruct the test run as Parallel or Serial.
Usually, we would set "read-only" tests as parallel and anything that modifies the cluster state as serial.

### Build Content image and create `ProfileBundle`

And then, we will need to have a content image to create a `ProfileBundle` for the e2e test, and we
can either use an existing content image or build one ourselves from your repo.

To build and use your content image, you need go to ComplianceAsCode/content repository.[1]
From the project’s root directory, execute the following command to build the container:

`podman build --no-cache --file Dockerfiles/ocp4_content --tag testcontent .`

You might need build dependencies in [2]
And then, you will need to login and push the built image to your repo, for example:

`podman push testcontent quay.io/username/content_repo:testcontent`

[1] https://github.com/openshift/compliance-operator/stargazers
[2] https://complianceascode.readthedocs.io/en/latest/manual/developer/02_building_complianceascode.html#installing-build-dependencies

Now we have the content image in a public repository on `quay.io/username/content_repo:testcontent`.

We can have following code to create ProfileBundle:

```go
                const baselineImage = "quay.io/username/content_repo:testcontent"
				pbName := getObjNameFromTest(t)
				prefixName := func(profName, ruleBaseName string) string { return profName + "-" + ruleBaseName }

				ocpPb := &compv1alpha1.ProfileBundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pbName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.ProfileBundleSpec{
						ContentImage: baselineImage,
						ContentFile:  ocpContentFile,
					},
				}
				if err := f.Client.Create(goctx.TODO(), ocpPb, getCleanupOpts(ctx)); err != nil {
					return err
				}
				if err := waitForProfileBundleStatus(t, f, namespace, pbName, compv1alpha1.DataStreamValid); err != nil {
					return err
				}

```

### Create `TailoredProfile`

Now we have the profile bundle ready, let's create an `TailoredProfile` as well.
We have following code for example to create a `TailoredProfile`:

```go
				suiteName := "kubeletconfig-test-node"
				scanName := "kubeletconfig-test-node-e2e"

				tp := &compv1alpha1.TailoredProfile{
					ObjectMeta: metav1.ObjectMeta{
						Name:      suiteName,
						Namespace: namespace,
					},
					Spec: compv1alpha1.TailoredProfileSpec{
						Title:       "kubeletconfig-Test",
						Description: "A test tailored profile to kubeletconfig remediation",
						EnableRules: []compv1alpha1.RuleReferenceSpec{
							{
								Name:      prefixName(pbName, requiredRule),
								Rationale: "To be tested",
							},
						},
						SetValues: []compv1alpha1.VariableValueSpec{
							{
								Name:      prefixName(pbName, "var-kubelet-evictionhard-imagefs-available"),
								Rationale: "Value to be set",
								Value:     "20%",
							},
						},
					},
				}
				mcTctx.ensureE2EPool()
				createTPErr := f.Client.Create(goctx.TODO(), tp, getCleanupOpts(ctx))
				if createTPErr != nil {
					return createTPErr
				}
```

If you notice the code above, `mcTctx.ensureE2EPool()` is optional, 
so this creates a single node sub-pool and labled as `pools.operator.machineconfiguration.openshift.io/e2e`
from a random worker node. This will significantly speed up the e2e 
testing time if the test contains remediations that will cause the node
to reboot, such as A machine config remediation as there is only one 
node that needs to be rebooted.


### Create `ScanSettingBinding`

Then we can create an `ScanSettingBinding`, the code is as following:

```go
				ssb := &compv1alpha1.ScanSettingBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      suiteName,
						Namespace: namespace,
					},
					Profiles: []compv1alpha1.NamedObjectReference{
						{
							APIGroup: "compliance.openshift.io/v1alpha1",
							Kind:     "TailoredProfile",
							Name:     suiteName,
						},
					},
					SettingsRef: &compv1alpha1.NamedObjectReference{
						APIGroup: "compliance.openshift.io/v1alpha1",
						Kind:     "ScanSetting",
						Name:     "e2e-default-auto-apply",
					},
				}
    			err = f.Client.Create(goctx.TODO(), ssb, getCleanupOpts(ctx))
				if err != nil {
					return err
				}
```

Because in the last section, we created sub `MachineConfigPool`, we 
need to use `e2e-default-auto-apply` as our `ScanSetting`, if there wasn't a
sub `MachineConfigPool`, we can use `default-auto-apply` as our  
`ScanSetting` instead.


### Run the test and wait for the remediation to be applied

Finally, we have following code to check if the test has been successfully run:

```go
	// Ensure that all the scans in the suite have finished and are marked as Done
				err = waitForSuiteScansStatus(t, f, namespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultNonCompliant)
				if err != nil {
					return err
				}

				// We need to check that the remediation is auto-applied and save
				// the object so we can delete it later
				remName := prefixName(scanName, requiredRule)
				waitForGenericRemediationToBeAutoApplied(t, f, remName, namespace)

				err = reRunScan(t, f, scanName, namespace)
				if err != nil {
					return err
				}

				// Scan has been re-started
				E2ELogf(t, "Scan phase should be reset")
				err = waitForSuiteScansStatus(t, f, namespace, suiteName, compv1alpha1.PhaseRunning, compv1alpha1.ResultNotAvailable)
				if err != nil {
					return err
				}

				// Ensure that all the scans in the suite have finished and are marked as Done
				E2ELogf(t, "Let's wait for it to be done now")
				err = waitForSuiteScansStatus(t, f, namespace, suiteName, compv1alpha1.PhaseDone, compv1alpha1.ResultCompliant)
				if err != nil {
					return err
				}
				E2ELogf(t, "scan re-run has finished")

				// Now the check should be passing
				checkResult := compv1alpha1.ComplianceCheckResult{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-kubelet-eviction-thresholds-set-hard-imagefs-available", scanName),
						Namespace: namespace,
					},
					ID:       "xccdf_org.ssgproject.content_rule_kubelet_eviction_thresholds_set_hard_imagefs_available",
					Status:   compv1alpha1.CheckResultPass,
					Severity: compv1alpha1.CheckResultSeverityMedium,
				}
				err = assertHasCheck(f, suiteName, scanName, checkResult)
				if err != nil {
					return err
				}

				E2ELogf(t, "The test succeeded!")
```

Noted that, in the example we used `waitForGenericRemediationToBeAutoApplied(t, f, remName, namespace)`, 
this will check if the remediation is auto-applied, and it will also wait for the `MachineConfigPool` to get updated. If the the remediaiton you are testing is a `MachineConfig` remediation, you can also use
`waitForRemediationToBeAutoApplied`. This will the correct `MachineConfig` object has been generated
by the remediaiton.

## How to run e2e test on your local cluster

In order to run e2e test on your local cluster, from the project’s root directory,
execute the following command:

`make e2e E2E_GO_TEST_FLAGS="-v -timeout 45m -run TestE2E/Parallel_tests/<testname>"`