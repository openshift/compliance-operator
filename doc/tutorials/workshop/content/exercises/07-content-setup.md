---
Title: Creating new content
PrevPage: 06-troubleshooting
NextPage: 07-creating-profile-bundles
---
Setup
-----

The compliance-operator uses the [ComplianceAsCode/content](https://github.com/ComplianceAsCode/content)
project to compile and generate the security content (in the form of a data
stream). When planning to modify this content, it is necessary to clone
the project:

```
host$ git clone https://github.com/ComplianceAsCode/content.git
host$ cd content
```

You need to make sure you have the environment ready that allows
compiling and publishing new content.

Using VSCode for content development is a good choice as there exist plugins
that make browsing the content easy.

In short, the machine you will be building on must be Linux-based as building
the content requires some Linux-specific packages such as `openscap-utils`.
If you are already running Linux and can install additional packages, the
setup is just a matter of running a couple of `yum/dnf` calls and cloning a
repo. You can head to the [SCAP developer
documentation](https://github.com/ComplianceAsCode/content/blob/master/docs/manual/developer_guide.adoc#building-complianceascode)
for detailed instructions.

If you are running a different OS, like macOS or can't install packages on
your system, we recommend that you use a VM for development. In this case,
using VSCode with the remote development plugin is also helpful.

Build VM Setup
--------------
You can use any VM hypervisor or provisioning you like, but for easier start, we have
prepared a vagrant box which already has all the build dependencies installed. The
vagrant box is shipped for both virtualbox and libvirt. Note that the image is rather
large (about 1GB).

If you don't have vagrant, install it on Fedora:
```
host$ dnf -y install vagrant
```
or on other systems install from their [upstream releases](https://www.vagrantup.com/downloads)


Once you have vagrant installed, create a file named `Vagrantfile` with the following
contents:
```
Vagrant.configure("2") do |config|
  config.vm.box = "jhrozek/compliance-devel"
  # Optional, feel free to use a different address
  config.vm.network "private_network", ip: "192.168.122.101"
end
```
start the VM from the same directory and verify the host is up:
```
host$ vagrant up
host$ vagrant ssh
```
When you're finished with the machine, you can shut it down with:
```
host$ vagrant halt
```
Or destroy completely with:
```
host$ vagrant destroy
```

If you used the `private_network` setting in your Vagrantfile, you can
also assign a hostname to your VM to access it easily:
```
host$ sudo echo "192.168.122.101  compliance" >> /etc/hosts
# on Fedora/RHEL, also reload dnsmasq or systemd-resolved
host$ sudo pkill -HUP dnsmasq
```
It's a good idea to copy your SSH key for the vagrant user so you can establish connections
without retyping the password over and over again:
```
host$ ssh-copy-id vagrant@compliance
```
The password for the `vagrant` user is also `vagrant` and the user has full sudo rights.
At this point, you should be able to ssh to your content builder machine:
```
host$ ssh vagrant@compliance
```

Once on the machine, head to the `content` directory and make sure the content can be built:
```
vagrant@compliance$ cd content
vagrant@compliance$ git pull
vagrant@compliance$ ./build_product rhcos4
```

Finally, make sure you can push images to your cluster. The `vagrant` user would by default
use the `KUBECONFIG` file at `~vagrant/kubeconfig`, so you can just copy it there from your
host machine:
```
host$ scp $KUBECONFIG vagrant@compliance:
```

This should allow us to even publish the content to our cluster:
```
vagrant@compliance$ cd content
vagrant@compliance$ ./utils/build_ds_container.sh
```
This command might take a fair amount of time, but eventually would finish with:
```
Success!
********
Your image is available at: image-registry.openshift-image-registry.svc:5000/openshift-compliance/openscap-ocp4-ds:latest
```

At this point, you are ready to experiment with your own Compliance-As-Code content!

VSCode Setup
-------------
While you are of course free to use any environment you like for editing the content,
VSCode ships with extensions that make it easy to both navigate the content and develop
on a remote VM.

Install VSCode
--------------
On a Linux distribution with FlatPak, you can install VSCode [from flathub](https://flathub.org/apps/details/com.visualstudio.code),
otherwise use the [official releases](https://code.visualstudio.com/Download).

Connect to a remote VM workspace
--------------------------------
This is useful only if you are developing in a VM and not on your local machine.
Install the extension called "Remote-SSH" with the ID `ms-vscode-remote.remote-ssh`.
Once installed, you should see an icon called "Remote explorer" in the toolbar
on the left side. Clicking it expands a toolbar with "SSH targets". Click the "+"
icon and enter your VM details (`ssh vagrant@compliance` if you are using the provided
Vagrant image). This adds a new target to the "SSH targets" list. Double-clicking
the folder icon next to the hostname opens a new VSCode window that is connected
to your host.

Open the compliance content files by clicking the Open icon (Top left on the sidebar)
and selecting "Open Folder". If you're using our vagrant boxes, open the directory
`/home/vagrant/content`. The CIS content is located in `ocp4/profiles/cis.profile`.

You can even open a terminal in VSCode that proxies to the remote machine by selecting
"Terminal" which should open a shell in the remote VM.

ComplianceAsCode extensions
---------------------------
The ComplianceAsCode extensions written by Gabriel Becker allow you to
navigate between rules and remediations. The extension is called "Content
Navigator" and its ID is `ggbecker.content-navigator`.  You should be able to
install it from the Extensions Marketplace as any other VSCode extension.

Note - if you are using a remote connection to a VM, you need to install the
extension in the window that opens for the remote connection as well.

If you open the CIS profile at `ocp4/profiles/cis.profile`, you can test the
extension by e.g. right-clicking a rule name and selecting "Open Rule". All
the extension features also have keyboard shortcuts.

***

In the next section we'll learn how to [create our own content images and
`ProfileBundles`](08-creating-profile-bundles.md) which will help us write
our own content.