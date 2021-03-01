---
Title: Troubleshooting
PrevPage: 07-content-setup
NextPage: 09-writing-rules
---
Creating your own ProfileBundles
--------------------------------

When writing your own content, you'll need to create your own content image.
If you already need to do this, you might as well create your own `ProfileBundle`.

The requirements for creating your own content image are quite simple. All
you need to do is create a container image with the data stream XML file
on the root of the container's file system, and that container image must
contain the `cp` binary. The `cp` utility is used to copy the data stream file
from the container image to a volume so the scanner can take it into use.

In the [ComplianceAsCode/content](https://github.com/ComplianceAsCode/content)
project there already is a utility that will help you create such images,
upload them automatically to your own OpenShift cluster, and even automatically
create `ProfileBundles` from these images. The aforementioned utility is:
[utils/build_ds_container.sh](https://github.com/ComplianceAsCode/content/blob/master/utils/build_ds_container.sh)

Before running the utility, make sure that you're able to build content at all.
From the root of the **content** repo, run:

```
$ ./build_product ocp4 rhcos4
```

If the command is successful, feel free to simply run the utility:

```
$ ./utils/build_ds_container.sh
```

This will build the content and upload it to an `ImageStream` in your
OpenShift cluster.

To ask the utility to create `ProfileBundles` from the built content,
you can call the utility as follows:

```
$ ./utils/build_ds_container.sh -p
```

The data streams take some time to parse, you can see as the profiles get
created with:

```
$ oc get profiles.compliance -w
NAME                     AGE
ocp4-cis                 13m
ocp4-e8                  13m
ocp4-moderate            13m
ocp4-ncp                 13m
rhcos4-e8                13m
rhcos4-moderate          13m
rhcos4-ncp               13m
upstream-ocp4-e8         5s
upstream-ocp4-moderate   5s
upstream-ocp4-ncp        5s
upstream-rhcos4-e8       0s
upstream-rhcos4-ncp      0s
upstream-rhcos4-moderate   0s
```

You'll see them appear as time passes.

With that in place, you can now modify, build the content using the `./utils/build_ds_container.sh`
script, and changes will be captured by the `ProfileBundle` automatically!.

***

Finally, we can move to the last topic which is about [writing your own rules
](09-writing-rules.md).
