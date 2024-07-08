/title Rollout Git version vX.XX.X

## Changelog

<!--
Add the changelog related to the new version and how this impacts us. It is especially important to highlight changes that increase the risk for this particular upgrade.
Would be really nice to point out contributions made by the Gitaly team, if any.
-->

## Steps

- [ ] Introduce the new Git version behind a feature flag ([Reference](https://gitlab.com/gitlab-org/gitaly/-/merge_requests/5587)).
  - [ ] Introduce the new bundled Git version in the [Makefile](/Makefile).
  - [ ] Introduce the new bundled Git execution environment in the [Git package](/internal/git/execution_environment.go) behind a feature flag.
- [ ] Roll out the feature flag.
  - [ ] Create an issue for the rollout of the feature flag ([Reference](https://gitlab.com/gitlab-org/gitaly/-/issues/5030)).
  - [ ] Optional: Create a [change request](https://about.gitlab.com/handbook/engineering/infrastructure/change-management/#change-request-workflows) in case the new Git version contains changes that may cause issues.
  - [ ] Roll out the feature flag.
  - [ ] Once the upgrade is deemed stable, remove the feature flag. You can do this in any release, including the release that removes the feature flag.
- [ ] Update the default Git version.
  - [ ] Remove the old bundled Git execution environment.
  - [ ] Remove the old bundled Git version in the [Makefile](/Makefile).
  - [ ] Update the default Git distribution by updating `GIT_VERSION` to the new Git version in the [Makefile](/Makefile).
- [ ] Optional: Upgrade the minimum required Git version. This is only needed when we want to start using features that have been introduced with the new Git version.
  - [ ] Update the minimum required Git version in the [Git package](/internal/git/version.go). ([Reference](https://gitlab.com/gitlab-org/gitaly/-/merge_requests/5705))
  - [ ] Update the minimum required Git version in the [README.md](/README.md).
  - [ ] Update the GitLab release notes to reflect the new minimum required Git version. ([Reference](https://gitlab.com/gitlab-org/gitlab/-/merge_requests/107565).

/label ~"Category:Gitaly" ~"group::gitaly" ~"group::gitaly::git" ~"section::enablement" ~"devops::systems" ~"type::maintenance" ~"maintenance::dependency"
/label ~"workflow::ready for development"
