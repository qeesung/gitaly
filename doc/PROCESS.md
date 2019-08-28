## Gitaly Team Process

### Gitaly Releases

Gitaly uses [SemVer](https://semver.org) version numbering.

#### Branching Model

All `vX.Y.0` tags get created on the `master` branch. We only make patch
releases when targeting a GitLab stable branch. Patch releases
(`vX.Y.1`, `vX.Y.2`, ...) must be made on stable branches (`X-Y-stable`)
in the Gitaly repository.

There should be **no patch releases on `master`**. Gitaly patch releases should
only be used for GitLab stable branches. If the release is not for a
GitLab stable branch, just increment the minor (or major) version
counter.

#### Creating a release

- Pick a release number (x.y.z)
- Check out the master branch on your local machine
- run `_support/release x.y.z`

Where x.y.z is a semver-compliant version number.

- To automatically create a merge-request in Gitlab CE to update that
  project to the latest tag, run

```shell
GITLAB_TOKEN=$(cat /path/to/gitlab-token) _support/update-downstream-server-version
```

- This will create a merge-request (with changelog) and assign it to you. Once the build has
  completed successfully, assign it to a maintainer for review.

##### Security release

Security releases involve additional processes to ensure that recent releases
of GitLab are properly patched while avoiding the leaking of the security
details to the public until appropriate.

Before beginning work on a security fix, open a new Gitaly issue with the template
`Security Release` and follow the instructions at the top of the page for following
the template.

## Experimental builds

Push the release tag to dev.gitlab.org/gitlab/gitaly. After the
passing the test suite, the tag will automatically be built and
published in https://packages.gitlab.com/gitlab/unstable.
