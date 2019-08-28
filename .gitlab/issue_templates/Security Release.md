<!-- Title suggestion: [Security Release] Release process for Gitaly issue #<issue-number> -->

## What

Release Gitaly security fixes into stable branches and master at the correct times.

## Owners

- Team: Gitaly
- Most appropriate slack channel to reach out to: `#g_gitaly`
- Best individuals to reach out to (note: may be the same person for both roles):
  - Contributor (developing fixes): `{replace with gitlab @ handle}`
  - Maintainer (releasing fixes): `{replace with gitlab @ handle}`

## Process

Follow this process precisely or else GitLab will implode and it will be all your fault (no pressure).

### DO NOT PUSH TO GITLAB.COM!

**IMPORTANT:** All steps below involved with a security release should be done
in a dedicated local repository cloned from https://dev.gitlab.org/gitlab/gitaly
unless otherwise specified. Using a dedicated repository prevents leaking
security patches by restricting the pushes to `dev.gitlab.org` hosted origins.
As a sanity check, you can verify your repository only points to remotes in
`dev.gitlab.org` by running: `git remote -v`

- **Contributors:** When developing fixes, you must adhere to these guidelines:
   - [ ] Your branch name should start with `security-` to prevent unwanted
     disclosures on the public gitlab.com (this branch name pattern is protected).
   - [ ] Start your security merge request against master in Gitaly on `dev.gitlab.org`
   - [ ] Keep the MR in WIP state until instructed otherwise.
   - [ ] Once finished and approved, **DO NOT MERGE**. Merging into master
     will happen later after the security release is public.
- **Contributors:** Backport fixes
   - Note what version of Gitaly you're backporting by opening
   [`GITALY_SERVER_VERSION`][gitaly-ce-version] for each supported GitLab-CE release and perform the following:
      - [ ] Latest release: `vX.Y.Z`
      - [ ] Previous release: `vX.Y.Z`
      - [ ] Oldest supported release: `vX.Y.Z`
        - (note: oldest supported release may only be needed for critical
           vulnerabilities)
   1. Deter
- **Maintainers:** If stable branch `X-Y-stable` does not exist yet,
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
- Only after the security release occurs and the details are made public:
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

## Appendix

### GitLab-CE Matrix

|                          | GitLab-CE Branch           | Gitaly Version | Gitaly Stable Branch | Gitaly MR | GitLab MR |
|--------------------------|----------------------------|------------|-|-|-|
| Latest                   | master | `1-X-stable` | | |
| Most recent release      | 12.2   | `1-X-stable` | | | |
| Previous release         | 12.1   | `1-X-stable` | | | |
| Oldest supported release | 12.0   | `1-X-stable` | | | |

[gitlab-ce-master]: https://gitlab.com/gitlab-org/gitlab-ce/tree/master

/label ~Gitaly ~"security"

/confidential
