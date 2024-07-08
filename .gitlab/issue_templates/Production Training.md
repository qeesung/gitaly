# Gitaly Team production training

This is a collection of items that should prepare new team members to be effective in understanding production issues and thus join the [on-call rotation](https://about.gitlab.com/handbook/engineering/development/enablement/systems/gitaly#gitaly-oncall-rotation).

While this can be started at any time, team members should complete their onboarding first, and have some experience in the codebase before completing this process.

## Links

- [Debugging Gitaly](https://about.gitlab.com/handbook/engineering/development/enablement/systems/gitaly/debugging)

Please help correct, clarify, or otherwise improve any documentation you find lacking!

## Questions

**This is not a test, it's an interactive learning guide.**

Please edit this section like a workbook, adding not just the answer but also how you got there.

Discuss your answers with your mentor / onboarding buddy / any senior team member / any other onboarding team member.

### Statistics

- [ ] What was Gitaly's SLO availability last month?
- [ ] Which repository had the most errors in `gprd` last week?
- [ ] What was the top error they had? If it's a real bug, please file an issue. :slight_smile:
- [ ] What was the p95 latency of the `SSHUploadPack` RPC in `gstg` last week?

### Feature flags

- [ ] When was the last feature flag enabled on `gprd`?
- [ ] ...How would you roll it back?
- [ ] Are there any feature flags currently rolling out? What stage are they in?

### Releases

- [ ] There's a bug in `git` and it needs to be rolled back. Describe the process and prepare the necessary MR(s).
- [ ] What commit of Gitaly is running currently in `gprd`?

### Git Operations

- [ ] Clones are "slow"
  - [ ] How much memory was consumed for clones (http or ssh) for the gitlab-org/gitlab repository in the past hour?
  - [ ] How much CPU was consumed for all clones (http or ssh) for the gitlab-org/gitlab repository in the past hour?
  - [ ] How slow are the slowest clones taking on the gitlab-org/gitlab repository the past day?

## Follow-up activities

Link: [Gitaly Customer Issues](https://gitlab.com/gitlab-org/gitaly/-/issues/?sort=due_date&state=opened&label_name%5B%5D=Gitaly%20Customer%20Issue&first_page_size=100)

- [ ] Read through some recently closed customer issues and the investigation. Follow the reasoning and understand the fix.
- [ ] Join an ongoing investigation, or pick up a new incoming issue. (Add the current milestone and ~"workflow::in dev" while assignign the issue to yourself.) Ask for help and guidance shamelessly. :slight_smile:
- [ ] Monitor `#g_gitaly` for incoming questions, direct them to our [intake flow](https://handbook.gitlab.com/handbook/engineering/infrastructure/core-platform/systems/gitaly/#customer-issues)

## Finally

Add yourself to the oncall rotation by raising a MR.

FIXME: details

/confidential
/assign me
/cc @andrashorvath @jcaigitlab
