# RPC deprecation process for gitaly-proto

First create a deprecation issue at
https://gitlab.com/gitlab-org/gitaly/issues with the title `Deprecate
RPC FooBar`. Use label `Deprecation`. Below is a template for the
issue description.

```
We are deprecating RPC FooBar because **REASONS**.

- [ ] put a deprecation comment `// DEPRECATED: <ISSUE-LINK>` in ./proto **Merge Request LINK**
- [ ] find all client-side uses of RPC and list below
- [ ] update all client-side uses to no longer use RPC **ADD Merge Request LINKS**
- [ ] wait for a GitLab release in which the RPC is no longer occurring in client side code **LINK TO GITLAB-CE RELEASE TAG**
- [ ] delete the server side implementation of the old RPC in https://gitlab.com/gitlab-org/gitaly **Merge Request LINK**
```
