# terraform for gitaly ha demo

## Creating a demo cluster

### 1. Install Google Cloud SDK

- For most platforms, including macOS, use the [official
   guide](https://cloud.google.com/sdk/docs/quickstarts)
- For Arch Linux, go to [this
   AUR](https://aur.archlinux.org/packages/google-cloud-sdk)

### 2. Install Terraform

On macOS with homebrew, use `brew install terraform`. For other
platforms see [the Terraform download
page](https://www.terraform.io/downloads.html).

### 3. Run the script

```
./create-demo-cluster
```

This will open a browser to sign into GCP if necessary. Terraform will
print a plan and ask you to confirm it before it creates anything in
GCP.

When the script is done, `apt-get install gitlab-ee` is still busy
running in the background on your new VM's.

### 4. Manually create a CloudSQL instance

In principle Terraform can also create a CloudSQL instance for
Praefect but this is still work in progress. In the meantime, please
use the GCP web UI to create a CloudSQL Postgres instance for
Praefect.

### 5. Use SSH to manually configure the hosts

Updating the config for all the demo cluster hosts is not yet
automated. Please follow the documentation at
https://docs.gitlab.com/ee/administration/gitaly/praefect.html.

To see the list of IP's for your machines, run:

```
./print-info
```

## Destroying a demo cluster

When you run the command below Terraform will print a plan of things
to destroy, that you then have to confirm (or abort with Ctrl-C).

Be careful! Double check how many nodes are being destroyed, and what
their names are.

```
./destroy-demo-cluster
```
