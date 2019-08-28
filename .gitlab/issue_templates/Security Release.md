<!-- Title suggestion: [Security Release] Release process for Gitaly issue #<issue-number> -->

## What

Release Gitaly security fixes into stable and master branches for Gitaly and GitLab at the correct times.

## Owners

- Team: Gitaly
- Most appropriate slack channel to reach out to: `#g_gitaly`
- Best individuals to reach out to (note: may be the same person for both roles):
  - Contributor (developing fixes): `{replace with gitlab @ handle}`
  - Maintainer (releasing fixes): `{replace with gitlab @ handle}`

## Version Matrix

| GitLab-CE Branch | Gitaly Tag/Branch | Gitaly MR          |
|------------------|-------------------|--------------------|
| `master`         | `v1.Y.Z`          | <MR link>          |
| `12.X`           | `1-X-stable`      | <backport MR link> |
| `12.X`           | `1-X-stable`      | <backport MR link> |
| `12.X`           | `1-X-stable`      | <backport MR link> |

## Process

Follow this process precisely or else GitLab will implode and it will be all your fault and everyone will point and laugh at you (no pressure).

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
   - [ ] Note what version of Gitaly you're backporting by opening
     [`GITALY_SERVER_VERSION`][gitaly-ce-version] for each supported GitLab-CE fill out
     the version matrix above.
- **Contributor**: Determine if Gitaly stable branches exist for all needed
  fixes.
   - [ ] If all of them exist, mark the next section with a `[-]` to skip.
     Otherwise, reassign the maintainer to complete the next section.
- **Maintainer:** If a Gitaly stable branch `X-Y-stable` in the table above
  does not exist yet, perform the following steps in a repository cloned
  from `gitlab.com` (since we will rely on the public Gitaly repo to push
  these stable branches to `dev.gitlab.org`):
    - [ ] For each missing stable branch:
       1. `git checkout vX.Y.0`
       1. `git checkout -b X-Y-stable`
       1. `git push --set-upstream origin X-Y-stable`
    - Reassign to the contributor.
- **Contributor:**
   - [ ] Backport fixes:
      1. Manually squash all commits in your MR to Gitaly master and force push it to your feature branch on `dev.gitlab.org`.
      1. Cherry pick that squashed commit into a backport MR for all Gitaly target stable branches on `dev.gitlab.org`.
      1. Link all backport MR's into the above table.
      1. Reassign to Maintainer
- **Maintainer:** After each stable branch merge request is approved and
  merged, run the release script to release the new version:
    - [ ] For each backported MR:
       1. Ensure that `gitlab.com` is not listed in any of the remotes: `git remote -v`
       1. `git checkout X-Y-stable`
       1. `git pull`
       1. `_support/release X.Y.Z` (where `Z` is the new incremented patch version)
       1. Upon successful vetting of the release, the script will provide a
          command for you to actually push the tag
    - Reassign to contributor
- **Contributor:** Bump Gitaly in GitLab:
   - [ ] For each version of GitLab-CE in the table above, create an MR on both
     [GitLab-CE](https://dev.gitlab.org/gitlab/gitlabhq) and
     [GitLab-EE](https://dev.gitlab.org/gitlab/gitlab-ee on `dev.gitlab.org`
     to bump the version in `GITALY_SERVER_VERSION`. Make sure you follow the
     [usual security process](https://gitlab.com/gitlab-org/release/docs/blob/master/general/security/developer.md).
   - Reassign to maintainer

### Only after the security release occurs and the details are made public

1. **Maintainer**:
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
   - [ ] Push all the newly released security tags in
   `dev.gitlab.org` to the public gitlab.com instance:
      1. `git remote add gitlab.com git@gitlab.com:gitlab-org/gitaly.git`
      1. `git push gitlab.com vX.Y.Z` (repeat for each tag)
      1. `git remote remove gitlab.com`
      1. Ensure no origins exist that point to gitlab.com: `git remote -v`
   - [ ] There is a good chance the newly patched Gitaly master
     on `gitlab.com` will need to be used to patch the latest GitLab CE/EE.
     This will require running the [regular release process](#creating-a-release)
     on gitlab.com.

[gitaly-ce-version]: https://gitlab.com/gitlab-org/gitlab-ce/blob/master/GITALY_SERVER_VERSION

/label ~Gitaly ~"security"

/confidential
