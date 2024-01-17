# Terraform for Gitaly HA demo

## Prerequisites

### Google Cloud SDK

- For most platforms, including macOS, use the [official
   guide](https://cloud.google.com/sdk/docs/quickstarts)
- For Arch Linux, go to [this
   AUR](https://aur.archlinux.org/packages/google-cloud-sdk)

### Install Terraform

On macOS with homebrew, use `brew install terraform`. For other
platforms see [the Terraform download
page](https://www.terraform.io/downloads.html).

### Install Ansible

Please refer to [Ansible's
documentation](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html)
to install it on your system.

## Provision your cluster

### 1. Create cluster

```
./create-demo-cluster
```

This will open a browser to sign into GCP if necessary. Ansible will then ask
you a set of questions before it performs the deployment.

When the script is done, `apt-get install gitlab-ee` is still busy
running in the background on your new VM's.

One of the provisioned resources is the database, which can take up to 10
minutes to be created.

### 2. Configure cluster

```
./configure-demo-cluster
```

Configuration of the cluster has been automated via Ansible. The cluster
creation script has automatically created a `hosts.ini` file for use by
Ansible containing all necessary information to configure the cluster.

If you wish to manually configure the cluster, please consult
https://docs.gitlab.com/ee/administration/gitaly/praefect.html.

To see the list of IP's for your machines, run:

```
./print-info
```

### 3. Destroy cluster

When you run the command below Terraform will print a plan of things
to destroy, that you then have to confirm (or abort with Ctrl-C).

Be careful! Double check how many nodes are being destroyed, and what
their names are.

```
./destroy-demo-cluster
```
