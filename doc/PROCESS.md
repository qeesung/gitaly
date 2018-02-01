
## Plan

We use our issues board for keeping our work in progress up to date in a single place. Please refer to it to see the current status of the project.

### Version 1.0

Version 1.0 is what we need to run gitlab.com 100% on Gitaly, with no
NFS access to Git repositories anymore.

We expect that gitlab.com will only use a subset of all endpoints. We
may choose to defer migrating some endpoints until version 1.1.

Version 1.0 will not be done until all included endpoints are in
opt-out state, meaning that they are sufficiently performant and
bug-free.

### Version 1.1

Version 1.1 will conclude the migration project. This means that that
the only production (i.e. non-test) code in anywhere in GitLab that
touches Git repositories is in Gitaly. There will be no configuration
'knowledge' in the main GitLab Rails application anymore on where the
repositories are stored.

After version 1.1 we will stop vendoring gitlab-git into Gitaly.

### Backlog

Any feature that is not essential to version 1.0 (gitlab.com 100%
Gitaly) or version 1.1 (0% Git in gitlab-ee) will be deferred until
after version 1.1.

### Order of Migration

Current priorities:

1. Work that gets us closer to version 1.0: issues with the [v1.0 label](https://gitlab.com/gitlab-org/gitaly/issues?label_name%5B%5D=v1.0)
1. Work towards version 1.1: issues with the [v1.1 label](https://gitlab.com/gitlab-org/gitaly/issues?label_name%5B%5D=v1.1)

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
