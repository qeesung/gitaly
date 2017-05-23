
## Plan

We use our issues board for keeping our work in progress up to date in a single place. Please refer to it to see the current status of the project.

1. [Absorb gitlab_git](https://gitlab.com/gitlab-org/gitlab-ce/issues/24374)
1. [Milestone 0.1](https://gitlab.com/gitlab-org/gitaly/milestones/2)
1. [Move more functions in accordance with the iterate process, starting with the ones with have the highest impact.](https://gitlab.com/gitlab-org/gitaly/issues/13)
1. [Change the connection on the workers from a unix socket to an actual TCP socket to reach Gitaly](https://gitlab.com/gitlab-org/gitaly/issues/29)
1. [Build Gitaly fleet that will have the NFS mount points and will run Gitaly](https://gitlab.com/gitlab-org/gitaly/issues/28)
1. [Move to GitRPC model where GitLab is not accessing git directly but through Gitaly](https://gitlab.com/gitlab-org/gitaly/issues/30)
1. [Remove the git NFS mount points from the worker fleet](https://gitlab.com/gitlab-org/gitaly/issues/27)
1. [Remove gitlab git from Gitlab Rails](https://gitlab.com/gitlab-org/gitaly/issues/31)
1. [Move to active-active with Git Ketch, with this we can read from any node, greatly reducing the number of IOPS on the leader.](https://gitlab.com/gitlab-org/gitlab-ee/issues/1381)
1. [Move to the most performant and cost effective cloud](https://gitlab.com/gitlab-com/infrastructure/issues/934)

## Iterate

[More on the Gitaly Process here](doc/PROCESS.md)

Instead of moving everything to Gitaly and only then optimize performance we'll iterate so we quickly have results

The iteration process is as follows:

1. Move a specific set of functions from Rails to Gitaly without performance optimizations (needs to happen before release, there is a switch to use either Rails or Gitaly)
1. Measure their original performance
1. Try to improve the performance by reducing reads and/or caching
1. Measure the effect and if warrented try again
1. Remove the switch from Rails

Some examples of a specific set of functions:

- The initial one is discussed in https://gitlab.com/gitlab-org/gitaly/issues/13
- File cache for Git HTTP GET /info/refs https://gitlab.com/gitlab-org/gitaly/issues/17
- Getting the “title” of a commit so we can use it for Markdown references/links
- Loading a blob for syntax highlighting
- Getting statistics (branch count, tag count, etc), ideally without loading all kinds of Git references (which currently happens)
- Checking if a blob is binary, text, svg, etc
- Blob cache seems complicated https://gitlab.com/gitlab-org/gitaly/issues/14

Based on the  [daily overview dashboard](http://performance.gitlab.net/dashboard/db/daily-overview?panelId=14&fullscreen), we should tackle the routes in `gitlab-rails` in the following order:

### Order of Migration

Using [data based on the](#generating-prioritization-data) [daily overview dashboard](http://performance.gitlab.net/dashboard/db/daily-overview?panelId=14&fullscreen),
we've prioritised the order in which we'll work through the `gitlab-rails` controllers
in descending order of **99% Cumulative Time** (that is `(number of calls) * (99% call time)`).

A [Google Spreadsheet](https://docs.google.com/spreadsheets/d/1MVjsbLIjBVryMxO0UhBWebrwXuqpbCz18ZtrThcSFFU/edit) is available
with these calculations.


### Generating the Priorization Data

Use this script to generate a CSV of the 99th percentile accumulated for a 7 day period.

This data will change over time, so it's important to reprioritize from time-to-time.

```shell
(echo 'Controller,Amount,Mean,p99,p99Accum'; \
influx \
  -host performance.gitlab.net \
  -username gitlab \
  -password $GITLAB_INFLUXDB_PASSWORD \
  -database gitlab \
  -execute "SELECT sum(count) as Amount, mean(duration_mean) AS Mean, mean(duration_99th) AS p99, sum(count) * mean(duration_99th) as Accum FROM downsampled.rails_git_timings_per_action_per_day WHERE time > now() - 7d GROUP BY action" \
  -format csv | \
  grep -v 'name,tags,'| \
  cut -d, -f2,4,5,6,7| \
  sed 's/action=//' | \
  sort --general-numeric-sort --key=5 --field-separator=, --reverse \
) > data.csv
```


## Gitaly Team Process

Gitaly is a fairly unique service in GitLab in that is has no dependencies on [gitlab-rails](https://gitlab.com/gitlab-org/gitlab-ce) or its SQL database.

This means that we can iterate faster than the gitlab-rails project, adding improvements (particularly optimisations) at a faster rate.

### Gitaly Releases

![](https://docs.google.com/drawings/d/1TlvxINA7vVNru7r9FGtLumoLRUmGgwR673Gtsonowns/pub?w=960&h=720)
[Edit this diagram](https://docs.google.com/drawings/d/1TlvxINA7vVNru7r9FGtLumoLRUmGgwR673Gtsonowns/edit)

This release process will work, provided the intra-gitlab-release are *semver* *patch* releases that don't introduce breaking API changes.

The focus of these patch releases would be performance improvements. New functionality would be added in *semver minor* or *major* releases in lock-step with the GitLab release train.

### Iterative Process

![](https://docs.google.com/drawings/d/11KY4ef2A1w1cie_um-ROUJ1N3GyFuWwhNEHjCzglzbA/pub?w=1440&h=810)
[Edit this diagram](https://docs.google.com/drawings/d/11KY4ef2A1w1cie_um-ROUJ1N3GyFuWwhNEHjCzglzbA/edit)

The diagram explains most of the process.

* Two week long iterations, kickoff on a Monday
* Two milestones per iteration
* One to two releases per iteration

#### Integrating with the GitLab Release Cycle

The GitLab release cycle culminates in a monthly release on the 22nd of each month. The cut for this release currently happens on the 7th of the month. Since Gitaly will be using a shorter, two week fixed cycle, some planning will be needed to ensure that we have a new stable release ready for the cut-off date. This will happen at the iteration kick-off.

## Branching Model

Like other GitLab projects, Gitaly uses the [GitLab Workflow](https://docs.gitlab.com/ee/workflow/gitlab_flow.html)  branching model.

![](https://docs.google.com/drawings/d/1VBDeOouLohq5EqOrht_9IGgNGQ2D6WgW_O6TgKytU2w/pub?w=960&h=720)

[Edit this diagram](https://docs.google.com/a/gitlab.com/drawings/d/1VBDeOouLohq5EqOrht_9IGgNGQ2D6WgW_O6TgKytU2w/edit)

* Merge requests will target the master branch.
* If the merge request is an optimisation of the previous stable branch, i.e. the branch currently running on GitLab.com, the MR will be cherry picked across to the stable branch and deployed to Gitlab.com from there.

# Migration Process

The Gitaly team aim to migrate each feature across to Gitaly according to a standardised process - read about the [Gitaly Migration Process here](MIGRATION_PROCESS.md)
