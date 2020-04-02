variable "demo_region" { default = "us-east4" }
variable "demo_zone" { default = "us-east4-c" }
variable "praefect_demo_cluster_name" { }
variable "ssh_user" { }
variable "ssh_pubkey" { }
variable "os_image" { default = "ubuntu-os-cloud/ubuntu-1804-lts" }
variable "startup_script" {
  default = <<EOF
    set -e
    if [ -d /opt/gitlab ] ; then exit; fi

    curl -s https://packages.gitlab.com/install/repositories/gitlab/nightly-builds/script.deb.sh | sudo bash 
    sudo apt-get install -y gitlab-ee
  EOF
}
variable "gitaly_machine_type" { default = "n1-standard-2" }
variable "gitaly_disk_size" { default = "100" }
#variable "praefect_sql_password" { }

provider "google" {
  version = "~> 3.12"

  project = "gitlab-internal-153318"
  region  = var.demo_region
  zone    = var.demo_zone
}

# resource "google_sql_database_instance" "praefect_sql" {
#   name             = format("%s-praefect-postgresql", var.praefect_demo_cluster_name)
#   database_version = "POSTGRES_9_6"
#   region           = var.demo_region
# 
#   settings {
#     # Second-generation instance tiers are based on the machine
#     # type. See argument reference below.
#     tier = "db-f1-micro"
#   }
# }
# 
# output "praefect_postgresql_ip" {
#   value = google_sql_database_instance.praefect_sql.public_ip_address
# }

# resource "google_sql_user" "users" {
#   name     = "praefect"
#   instance = google_sql_database_instance.praefect_sql.name
#   password = var.praefect_sql_password
# }

resource "google_compute_instance" "gitlab" {
  name         = format("%s-gitlab", var.praefect_demo_cluster_name)
  machine_type = "n1-standard-2"

  boot_disk {
    initialize_params {
      image = var.os_image
      size = var.gitaly_disk_size
    }
  }

  network_interface {
    subnetwork = "default"
    access_config {}
  }

  metadata = {
    ssh-keys = format("%s:%s", var.ssh_user, var.ssh_pubkey)
    startup-script = var.startup_script
  }

  tags = ["http-server", "https-server"]
}

output "gitlab_internal_ip" {
  value = google_compute_instance.gitlab.network_interface[0].network_ip
}
output "gitlab_external_ip" {
  value = google_compute_instance.gitlab.network_interface[0].access_config[0].nat_ip
}

resource "google_compute_instance" "praefect" {
  name         = format("%s-praefect", var.praefect_demo_cluster_name)
  machine_type = "n1-standard-1"

  boot_disk {
    initialize_params {
      image = var.os_image
    }
  }

  network_interface {
    subnetwork = "default"
    access_config {}
  }

  metadata = {
    ssh-keys = format("%s:%s", var.ssh_user, var.ssh_pubkey)
    startup-script = var.startup_script
  }
}

output "praefect_internal_ip" {
  value = google_compute_instance.praefect.network_interface[0].network_ip
}
output "praefect_ssh_ip" {
  value = google_compute_instance.praefect.network_interface[0].access_config[0].nat_ip
}

resource "google_compute_instance" "gitaly" {
  count = 3
  name         =  "${var.praefect_demo_cluster_name}-gitaly-${count.index + 1}"
  machine_type = var.gitaly_machine_type

  boot_disk {
    initialize_params {
      image = var.os_image
      size = var.gitaly_disk_size
    }
  }

  network_interface {
    subnetwork = "default"
    access_config {}
  }

  metadata = {
    ssh-keys = format("%s:%s", var.ssh_user, var.ssh_pubkey)
    startup-script = var.startup_script
  }
}

output "gitaly_internal_ip" {
  value = {
    for instance in google_compute_instance.gitaly:
    instance.name => instance.network_interface[0].network_ip
  }
}

output "gitaly_ssh_ip" {
  value = {
    for instance in google_compute_instance.gitaly:
    instance.name => instance.network_interface[0].access_config[0].nat_ip
  }
}
