## Plan

We use our issues board for keeping our work in progress up to date in a single place. Please refer to it to see the current status of the project.

## Gitaly Team Process

### Gitaly Releases

Gitaly uses [SemVer](https://semver.org) version numbering.

## Branching Model

Like other GitLab projects, Gitaly uses the [GitLab Workflow](https://docs.gitlab.com/ee/workflow/gitlab_flow.html)  branching model.

![](https://docs.google.com/drawings/d/1VBDeOouLohq5EqOrht_9IGgNGQ2D6WgW_O6TgKytU2w/pub?w=960&h=720)

[Edit this diagram](https://docs.google.com/a/gitlab.com/drawings/d/1VBDeOouLohq5EqOrht_9IGgNGQ2D6WgW_O6TgKytU2w/edit)

* Merge requests will target the master branch.
* If the merge request is an optimisation of the previous stable branch, i.e. the branch currently running on GitLab.com, the MR will be cherry picked across to the stable branch and deployed to Gitlab.com from there.
