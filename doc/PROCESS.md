
## Plan

We use our issues board for keeping our work in progress up to date in a single place. Please refer to it to see the current status of the project.

1. [Absorb gitlab_git](https://gitlab.com/gitlab-org/gitlab-ce/issues/24374)
1. [Milestone 0.1](https://gitlab.com/gitlab-org/gitaly/milestones/2)
1. [Move to GitRPC model where GitLab is not accessing git directly but through Gitaly](https://gitlab.com/gitlab-org/gitaly/issues/30)
1. [Remove the git NFS mount points from the worker fleet](https://gitlab.com/gitlab-org/gitaly/issues/27)
1. [Remove gitlab git from Gitlab Rails](https://gitlab.com/gitlab-org/gitaly/issues/31)
1. [Move to active-active with Git Ketch, with this we can read from any node, greatly reducing the number of IOPS on the leader.](https://gitlab.com/gitlab-org/gitlab-ee/issues/1381)
1. [Move to the most performant and cost effective cloud](https://gitlab.com/gitlab-com/infrastructure/issues/934)

### Order of Migration

Current priorities:

1. Migrate all endpoints that are needed to make gitlab.com run 100% on Gitaly only (no NFS)
1. Solve all bugs, performance problems and N+1 queries that prevent us from running gitlab.com 100% on Gitaly
1. Migrate all the endpoints in gitlab-ee

## Gitaly Team Process

Gitaly is a fairly unique service in GitLab in that is has no dependencies on [gitlab-rails](https://gitlab.com/gitlab-org/gitlab-ce) or its SQL database.

This means that we can iterate faster than the gitlab-rails project, adding improvements (particularly optimisations) at a faster rate.

### Gitaly Releases

Gitaly is still below 1.0.0. We increment the minor version when adding new features, or the patch version for bug fixes.

### Iterative Process

![](https://docs.google.com/drawings/d/11KY4ef2A1w1cie_um-ROUJ1N3GyFuWwhNEHjCzglzbA/pub?w=1440&h=810)
[Edit this diagram](https://docs.google.com/drawings/d/11KY4ef2A1w1cie_um-ROUJ1N3GyFuWwhNEHjCzglzbA/edit)

The diagram explains most of the process.

* Two week long iterations, kickoff on a Wednesday
* Two milestones per iteration

## Branching Model

Like other GitLab projects, Gitaly uses the [GitLab Workflow](https://docs.gitlab.com/ee/workflow/gitlab_flow.html)  branching model.

![](https://docs.google.com/drawings/d/1VBDeOouLohq5EqOrht_9IGgNGQ2D6WgW_O6TgKytU2w/pub?w=960&h=720)

[Edit this diagram](https://docs.google.com/a/gitlab.com/drawings/d/1VBDeOouLohq5EqOrht_9IGgNGQ2D6WgW_O6TgKytU2w/edit)

* Merge requests will target the master branch.
* If the merge request is an optimisation of the previous stable branch, i.e. the branch currently running on GitLab.com, the MR will be cherry picked across to the stable branch and deployed to Gitlab.com from there.

# Migration Process

The Gitaly team aim to migrate each feature across to Gitaly according to a standardised process - read about the [Gitaly Migration Process here](MIGRATION_PROCESS.md)
