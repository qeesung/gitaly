# Gitaly documentation

The historical reasons for the inception of Gitaly and our design decisions are
written in [the design doc](DESIGN.md).

## Configuring Gitaly

Running Gitaly requires it to be configured correctly, options are described in
GitLab's  [configuration documentation](https://docs.gitlab.com/ee/administration/gitaly/index.html).

The reference guide is documented in <https://docs.gitlab.com/ee/administration/gitaly/reference.html>.

## Developing Gitaly

- When new to Gitaly development, start by reading the [beginners guide](beginners_guide.md)
- The Gitaly release process is described in [our process doc](PROCESS.md)
- Tests use Git repositories too, [read more about them](test_repos.md)
- Praefect uses SQL. To create a new SQL migration see [sql_migrations.md](sql_migrations.md)
- For Gitaly hooks documentation, see [Gitaly hooks documentation](hooks.md)
- To configure an environment for developing Gitaly on Kubernetes, see the
  [Gitaly on GKE Template](https://gitlab.com/gitlab-com/infra-standards/project-templates/gitaly-on-gke-template) repository.

## Gitaly Cluster

Gitaly does not replicate any data. If a Gitaly server goes down, any of its
clients can't read or write to the repositories stored on that server. This
means that Gitaly is not highly available. How this will be solved is described
[in the HA design document](design_ha.md)

For configuration please read [Praefect's configuration documentation](configuration/praefect.md).

## Technical explanations

- [Delta Islands](delta_islands.md)
- [Disk-based Cache](design_diskcache.md)
- [`gitaly-ssh`](../cmd/gitaly-ssh/README.md)
- [Git object quarantine during Git push](object_quarantine.md)
- [Logging in Gitaly](logging.md)
- [Tips for reading Git source code](reading_git_source.md)
- [Serverside Git Usage](serverside_git_usage.md)
- [Object Pools](object_pools.md)
- [Sidechannel protocol](sidechannel.md)
- [Backpressure](backpressure.md)

## RFCs

- [Praefect Queue storage](rfcs/praefect-queue-storage.md)
- [Snapshot storage](rfcs/snapshot-storage.md)
