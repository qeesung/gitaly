## Plan

We use our issues board for keeping our work in progress up to date in a single place. Please refer to it to see the current status of the project.

## Gitaly Team Process

### Gitaly Releases

Gitaly uses [SemVer](https://semver.org) version numbering.

## Branching Model

All `vX.Y.0` tags get created on the `master` branch. We only make patch
releases when targeting a GitLab stable branch. Patch releases
(`vX.Y.1`, `vX.Y.2`, ...) must be made on stable branches (`X-Y-stable`)
in the Gitaly repository.

There should be **no patch releases on `master`**. Gitaly patch releases should
only be used for GitLab stable branches. If the release is not for a
GitLab stable branch, just increment the minor (or major) version
counter.
