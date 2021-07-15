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
You can use any VM hypervisor or provisioning you like, Here we are going to use VirtualBox as an example:

1. You need to Download and Install VirtualBox from link below or you can use any hypervisor that you preferred.
https://www.virtualbox.org/wiki/Downloads

2. Download Linux image that you preferred, here we are going to use Fedora:

https://getfedora.org/en/server/download/ Link to Download

3. Setup VMs on VirtualBox

Open VirtualBox, click New, and use the following steps as a guide:

Give the VM a name,`compliance` in this case, choose Linux for the Type, and select the Linux version to Fedora in this case.

Assign 2048 MB or greater to the VM, click Next, choose `Create a virtual hard disk now`, then click Create.
You can leave everything as default, and put the size of hard disk to equal or greater than 20GB

Once the VM is created, select the VM you just created, and then right click goes to `Setting`, go to `Network`, 
In the `Adapter 1` Tab, change the `Attached to` Drop Down to `Host-Only Adapter`. Next, go to `Storage` and choose the 
host drive, and then change the oprical drive to the ISO Image file that you have just downloaded.

Click `Ok` to save the setting.

4. Configure the VM

Start the VM, complete the OS installation. 

After the OS installation, log into the system, open the terminal, type the following to install and enable SSH service 
and allow port 22 on firewall for remote access:

```
sudo dnf install openssh-server
sudo systemctl enable sshd.service --now
sudo firewall-cmd --zone=public --permanent --add-port 22/tcp
sudo firewall-cmd --reload
```

And then you can get your IP Address by using `ip a` command,
In my case my VM's IP is `192.168.56.101`. From here you can connect the VM through ssh.

Go to terminal on your host machine, connect the VM through ssh: `ssh USER@IP-OF-VM`.

Once on the machine, you can clone the Compliance AS Code repo:
`git clone https://github.com/ComplianceAsCode/content.git`

And then you will also need to install OpenShift CLI using tutorial here: 

https://docs.openshift.com/container-platform/4.7/cli_reference/openshift_cli/getting-started-cli.html

Once everything is ready, head to the `content` directory and make sure the content can be built:
```
user@compliance$ cd content
user@compliance$ git pull
user@compliance$ ./build_product rhcos4
```

Finally, make sure you can push images to your cluster. The `user` user would by default
use the `KUBECONFIG` file at `~user/kubeconfig`, so you can just copy it there from your
host machine:
```
host$ scp $KUBECONFIG user@compliance:
```

This should allow us to even publish the content to our cluster:
```
user@compliance$ cd content
user@compliance$ ./utils/build_ds_container.sh
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
icon and enter your VM details (`ssh user@compliance`). This adds a new target to 
the "SSH targets" list. Double-clicking the folder icon next to the hostname opens a 
new VSCode window that is connected to your host.

Open the compliance content files by clicking the Open icon (Top left on the sidebar)
and selecting "Open Folder". If you're using the VM setup, open the directory
`/home/user/content`. The CIS content is located in `ocp4/profiles/cis.profile`.

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