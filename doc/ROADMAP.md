# Gitaly Product Roadmap

## Gitaly Version 1.0

_Timeframe_: Q4 2017

**GitLab.com running with 100% of Git operations going through Gitaly**: no rugged operations or git process spawns from the Ruby monolith. NFS operations/sec to git file servers down to zero.

At this point, the GitLab Kubernetes Helm Charts can move towards running Gitaly as one-or-more StatefulSets. Since some features will be opt-in, these setup will be experimental.

## Gitaly Version 1.1

_Timeframe_: Q1 2018

**All features are mandatory**. Rugged code in `Gitlab::Git` is removed from the monolith and now only exists in Gitaly-Ruby.

Kubernetes Helm Charts using multiple Gitaly Git-Storage StatefulSets become standard.

## Gitaly Version 2

_Timeframe_: Q2 2018

This milestone will focus on optimizing the Gitaly endpoints through caching, moving from spawned child processes to in-process operations where appropriate.

## Future Plans

Once Gitaly is complete and fast, many new features can be added, but have yet to undergo prioritization:

In no particular order. 

* **Multiple replicas** of Git data, for high-availability
* **Repository repair**: fix missing refs, remove old tmp files etc
* Automatic **fault detection** and auto-migration of git data to healthy servers
* **Shared object databases** for optimized file storage requirements
* **Object-storage blob-store** backend option
* Per-user, per-group, per-repo CPU IOPS Memory resource **accounting** and **limiting**
* Auto **shard rebalancing** of repositories across replica sets based on accounting
* **Microsoft Git Virtual File System** support: https://gitlab.com/gitlab-org/gitlab-ce/issues/27895
* **Git-optimized indexing**. Customized indexing solution would allow us to index _all_ branches of a repository in time+cpu efficient manner.
* **Clonebundle support** for fast, efficient clones to specialized clients (for example, Gitlab CI): https://gitlab.com/gitlab-org/gitaly/issues/610
