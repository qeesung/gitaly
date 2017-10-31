# Gitaly release process

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

## Experimental builds

Push the release tag to dev.gitlab.org/gitlab/gitaly. After the
passing the test suite, the tag will automatically be built and
published in https://packages.gitlab.com/gitlab/unstable.
