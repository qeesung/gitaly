/title [Security Release] Release process for Gitaly issue #<issue-number>

## What

Release Gitaly security fixes into stable and master branches for Gitaly and
GitLab at the correct times.

## Owners

- Team: Gitaly
- Most appropriate slack channel to reach out to: `#g_gitaly`
- Best individuals to reach out to (note: may be the same person for both roles):
  - **Contributor** (developing fixes): `{replace with gitlab @ handle}`
  - **Maintainer** (releasing fixes): `{replace with gitlab @ handle}`

## Version Matrix

| GitLab Branch | Gitaly Branch  | Gitaly MR          |
|---------------|----------------|--------------------|
| `master`      | **TBD**        | <MR link>          |
| `12.X`        | `12-X-stable`  | <backport MR link> |
| `12.Y`        | `12-Y-stable`  | <backport MR link> |
| `12.Z`        | `12-Z-stable`  | <backport MR link> |

## Process


### DO NOT PUSH TO GITLAB.COM!

**IMPORTANT:** All steps below involved with a security release should be done
in a dedicated local repository cloned from https://dev.gitlab.org/gitlab/gitaly
unless otherwise specified. Using a dedicated repository prevents leaking
security patches by restricting the pushes to `dev.gitlab.org` hosted origins.
As a sanity check, you can verify your repository only points to remotes in
`dev.gitlab.org` by running: `git remote -v`

- **Contributor:** When developing fixes, you must adhere to these guidelines:
   - [ ] Your branch name should start with `security-` to prevent unwanted
     disclosures on the public gitlab.com (this branch name pattern is protected).
   - [ ] Start your security merge request against master in Gitaly on `dev.gitlab.org`
   - [ ] Keep the MR in WIP state until instructed otherwise.
   - [ ] Once finished and approved, **DO NOT MERGE**. Merging into master
     will happen later after the security release is public.
- **Contributor:** Backport fixes
   - [ ] Fill out the [version matrix](#version-matrix) above
     checking if all the versions are affected and require a fix
- **Contributor:**
   - [ ] Backport fixes:
      1. Manually squash all commits in your MR to Gitaly master and force push it to your feature branch on `dev.gitlab.org`.
      1. Cherry pick that squashed commit into a backport MR for all Gitaly target stable branches on `dev.gitlab.org`.
      1. Link all backport MR's into the [above table](#version-matrix).
      1. Reassign to Maintainer
- **Maintainer:**
    - [ ] Review and merge each stable branch merge request
    - tagging and version bump will be automated by `release-tools`

### Only after the security release occurs and the details are made public

- **Maintainer**:
   - [ ] Check mirroring status with chatops in slack `/chatops run mirror status`
   - [ ] Ensure master branch on dev.gitlab.com is synced with gitlab.com:
      1. `git checkout master`
      1. `git remote add gitlab.com git@gitlab.com:gitlab-org/gitaly.git`
      1. `git pull gitlab.com master`
      1. `git push origin`
      1. `git remote remove gitlab.com`
      1. Ensure no origins exist that point to gitlab.com: `git remote -v`
   - [ ] Merge in request against master on `dev.gitlab.com`
   - [ ] Bring gitlab.com up to sync with dev.gitlab.org:
      1. `git remote add gitlab.com git@gitlab.com:gitlab-org/gitaly.git`
      1. `git fetch gitlab.com`
      1. `git checkout -b gitlab-com-master gitlab.com/master`
      1. `git merge origin/master` (note: in this repo, origin points to dev.gitlab.org)
      1. `git push gitlab.com gitlab-com-master:master`
          - Note: If the push fails, try running `git pull gitlab.com master`
            and then try the push again.
   - [ ] Upon success, remove the branch and remote:
      1. `git checkout master`
      1. `git branch -D gitlab-com-master`
      1. `git remote remove gitlab.com`
      1. Ensure no origins exist that point to gitlab.com: `git remote -v`
   - [ ] There is a good chance the newly patched Gitaly master
     on `gitlab.com` will need to be used to patch the latest GitLab CE/EE.
     This will require running the regular release candidate process on gitlab.com.
   - [ ] Gitaly on GitLab.com uses push mirroring to dev.gitlab.com, if branches
   are diverged this stops working. Go to `Settings > Repository > Mirroring repositories`
   to update the mirror. When there's no error after the manual update, it will
   resume normal operation.

[gitaly-ce-version]: https://gitlab.com/gitlab-org/gitlab-ce/blob/master/GITALY_SERVER_VERSION
[gitlab-sec-process]: https://gitlab.com/gitlab-org/release/docs/blob/master/general/security/developer.md

/label ~"devops::create" ~"group::gitaly" ~"security"

/confidential
