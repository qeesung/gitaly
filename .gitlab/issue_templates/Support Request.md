# Support Request for the Gitaly Team

<!--

The goal of this template is to create a consistent experience for customer support requests from the Gitaly Team. Due to the size of the team and ambitious amount of work we try to complete, it helps us tremendously to have a common issue format for requests that can be prioritized appropriately. It also helps keep a record of issues experienced that can benefit other teams in the future.

As we collaborate on resolution of this issue, the Gitaly team will attempt to utilize this as a single source of truth.

-->

_The goal is to keep these requests public. However, if customer information is required to the support request, please be sure to mark this issue as confidential._

This request template is part of [Gitaly Team's intake process](https://about.gitlab.com/handbook/engineering/development/enablement/systems/gitaly/#how-to-contact-the-team).


## Author Checklist

- [ ] Reached out to [#spt_pod_git](https://gitlab.enterprise.slack.com/archives/C04D5FUADAM) prior creating issue (please provide link)
- [ ] Fill out customer information section
    - [ ] Provide an detail summary under **Additional Information:**
- [ ] Severity realistically set
- [ ] Provided detailed problem description
- [ ] Provided detailed troubleshooting performed
- [ ] Clearly articulated what is needed from the Gitaly team to support your request by filling out the _What specifically do you need from the Gitaly team_

## Customer Information

**Salesforce Link:**

**Zendesk Ticket:**

**Installation Size:**

**Architecture Information:**
<!-- Please include cloud hosting provider if available, links to architecture documents, etc... -->
**Slack Channel:**
<!-- Please include the general slack channel, the slack channel for the incident, etc... -->
**Additional Information:**
<!-- Links to executive summary, customer calls, etc... Anything that helps provide context for the team -->

## Support Request

### Severity

<!-- Please be as realistic as possible here. We are sensitive to the fact that customers are frustrated when things aren't working, but realistically we cannot treat everything as a Severity 1 emergency.

For a good rule of thumb, please refer to the bug prioritization framework located in the handbook here: https://about.gitlab.com/handbook/engineering/quality/issue-triage/#severity

For S1 or S2 issues, please follow https://about.gitlab.com/handbook/engineering/development/enablement/systems/gitaly/#urgent-issues-and-outages .
-->

### Problem Description

<!-- Please describe the problem in as much detail as possible. Feel free to include log outputs, screenshots, or anything else that could help the team understand what is happening. -->

- What version is the customer running?
- What is the customers architecture?
    - What is the GitLab architecture?
    - Are networking filesystems (like NFS) used?
    - What are the filesystems?
    - What are the OS and kernel versions?
    - How are backup, replication, HA, etc performed?
- Are they using Gitaly Cluster?
    - How many Gitaly Clusters the customer has?
    - How many Gitaly nodes per cluster the customer has configured?
- Has the customer, or some tools/script (backup, synchro, replication, HA, etc) they set up, directly interacted with the Git repository? 
    - using `rsync` or similar tools?
    - `git` commands?
    - history changing tools (like [git filter-repo](https://github.com/newren/git-filter-repo))?
- Does the customer have any hooks configured?
    - [Git server hooks](https://docs.gitlab.com/ee/administration/server_hooks.html) 
    - [Git hooks](https://git-scm.com/book/en/v2/Customizing-Git-Git-Hooks)
- If this is a performance issue, what does the Git workflow look like?
    - What are the customer RPS for push and pulls? (use [fast-stats](https://gitlab.com/gitlab-com/support/toolbox/fast-stats))
    - How mamy pipelines does the customer run?
    - How many users are working on the instance?
    - How big are the repositories? Do they have [monorepos](https://docs.gitlab.com/ee/user/project/repository/monorepos/)?
        - Provide the output of [git-sizer](https://github.com/github/git-sizer). 

### Troubleshooting Performed

<!-- Please include any initial troubleshooting performed by the customer support or professional services teams -->

### What specifically do you need from the Gitaly team

<!-- Please include specifics such as - architecture review, meeting attendance, product management involvement, etc... -->


/label ~"Gitaly Customer Issue" ~"customer" ~"group::gitaly" ~"devops::systems" ~"section::enablement" ~"workflow::problem validation"
/cc @mjwood @andrashorvath @jcaigitlab @john.mcdonnell @gerardo
