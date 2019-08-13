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

## DO NOT PUSH TO GITLAB.COM!

**IMPORTANT:** All steps below involved with a security release should be done
in a dedicated local repository cloned from https://dev.gitlab.org/gitlab/gitaly
unless otherwise specified. Using a dedicated repository prevents leaking
security patches by restricting the pushes to `dev.gitlab.org` hosted origins.
As a sanity check, you can verify your repository only points to remotes in
`dev.gitlab.org` by running: `git remote -v`

1. **Contributors:**
   - Start your security merge request against master in Gitaly on dev.gitlab.org
   - Your branch name should start with `security-` to prevent unwanted
     disclosures on the public gitlab.com (this branch name pattern is protected).
   - Once finished and approved, **DO NOT MERGE**. Merging into master
     will happen later after the security release is public.
1. **Contributors:** For each supported version of GitLab-CE, note what version
   of Gitaly you're backporting by opening
   [`GITALY_SERVER_VERSION`][gitaly-ce-version] and perform the following:
    1. **Maintainers:** If stable branch `X-Y-stable` does not exist yet,
       perform the following steps in a repository cloned
       from `gitlab.com` (since we will rely on the public Gitaly repo to push
       these stable branches to `dev.gitlab.org`):
        1. `git checkout vX.Y.0`
        1. `git checkout -b X-Y-stable`
        1. `git push --set-upstream origin X-Y-stable`
    1. **Contributors:** Using cherry picked feature commits (not merge commits) from your approved MR
       against master, create the required merge requests on `dev.gitlab.org`
       against each stable branch.
    1. **Maintainers:** After each stable branch merge request is approved and
       merged, run the release script to release the new version:
        1. Ensure that `gitlab.com` is not listed in any of the remotes:
           `git remote -v`
        1. `git checkout X-Y-stable`
        1. `git pull`
        1. `_support/release X.Y.Z` (where `Z` is the new incremented patch version)
        1. Upon successful vetting of the release, the script will provide a
           command for you to actually push the tag
    1. **Contributors:** Bump `GITALY_SERVER_VERSION` on the client
       (gitlab-rails) in each backported merge request against both
       [GitLab-CE](https://dev.gitlab.org/gitlab/gitlabhq)
       and [GitLab-EE](https://dev.gitlab.org/gitlab/gitlab-ee).
        - Follow the [usual security process](https://gitlab.com/gitlab-org/release/docs/blob/master/general/security/developer.md)
1. Only after the security release occurs and the details are made public:
    1. **Maintainers** Ensure master branch on dev.gitlab.com is synced with gitlab.com:
       1. `git checkout master`
       1. `git remote add gitlab.com git@gitlab.com:gitlab-org/gitaly.git`
       1. `git pull gitlab.com master`
       1. `git push origin`
       1. `git remote remove gitlab.com`
       1. Ensure no origins exist that point to gitlab.com: `git remote -v`
    1. **Contributors:** Merge in your request against master on dev.gitlab.com
    1. **Maintainers:** Bring gitlab.com up to sync with dev.gitlab.org:
       1. `git remote add gitlab.com git@gitlab.com:gitlab-org/gitaly.git`
       1. `git fetch gitlab.com`
       1. `git checkout -b gitlab-com-master gitlab.com/master`
       1. `git merge origin/master` (note: in this repo, origin points to dev.gitlab.org)
       1. `git push gitlab.com gitlab-com-master:master`
       1. If the push fails, try running `git pull gitlab.com master` and then
          try the push again.
       1. Upon success, remove the branch and remote:
          1. `git checkout master`
          1. `git branch -D gitlab-com-master`
          1. `git remote remove gitlab.com`
          1. Ensure no origins exist that point to gitlab.com: `git remote -v`
    1. **Maintainers:** Push all the newly released security tags in
       `dev.gitlab.org` to the public gitlab.com instance:
       1. `git remote add gitlab.com git@gitlab.com:gitlab-org/gitaly.git`
       1. `git push gitlab.com vX.Y.Z` (repeat for each tag)
       1. `git remote remove gitlab.com`
       1. Ensure no origins exist that point to gitlab.com: `git remote -v`
    1. **Maintainers:** There is a good chance the newly patched Gitaly master
       on `gitlab.com` will need to be used to patch the latest GitLab CE/EE.
       This will require running the [regular release process](#creating-a-release)
       on gitlab.com.

[gitaly-ce-version]: https://gitlab.com/gitlab-org/gitlab-ce/blob/master/GITALY_SERVER_VERSION

## Experimental builds

Push the release tag to dev.gitlab.org/gitlab/gitaly. After the
passing the test suite, the tag will automatically be built and
published in https://packages.gitlab.com/gitlab/unstable.
