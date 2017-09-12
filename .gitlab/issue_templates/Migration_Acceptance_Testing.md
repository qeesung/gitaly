~Conversation: #CONVERSATION_NUMBER

See the [Migration Process documentation](https://gitlab.com/gitlab-org/gitaly/blob/master/doc/MIGRATION_PROCESS.md#acceptance-testing-acceptance-testing)
for more information on the Acceptance Testing stage of the process.

## Details
- **Feature Toggle Name**: `GITALY_FEATURE_NAME`
- **GRPC Service**: `GRPC_SERVICE_NAME::GRPC_METHOD_NAME`
- **Required Gitaly Version**: `vX.X.X`
- **Required GitLab Version**: `vX.X`

--------------------------------------------------------------------------------

## 1. Preparation

- [ ] **Routes**: what routes use this migration?
  1. Please list a set of routes that are known to use this endpoint...
  2. ...
  3. ...

## 2. Development Trial

#### Check Dev Server Versions
- [ ] Gitaly: [Gitaly Dev Version Tracker Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-version-tracker?orgId=1&var-job=gitaly-dev)
- [ ] GitLab: https://dev.gitlab.org/help

#### Enable on `dev.gitlab.org`:
- [ ] `!feature-set GITALY_FEATURE_NAME true` in [`#dev-gitlab`](https://gitlab.slack.com/messages/C6WQ87MU3)

Then leave running while monitoring and performing some testing through web, api or SSH.

#### Monitor (initially )

- [ ] **Monitor Grafana** feature dashboard on dev: [Gitaly Feature Status Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-feature-status?from=now-12h&to=now&orgId=1&var-method=GRPC_METHOD_NAME&var-job=gitaly-dev&refresh=5m)
- [ ] **Inspect logs** in ELK:
  - [GRPC_METHOD_NAME invocations, last hour](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20hostname:dev'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-1h,mode:quick,to:now%29%29) for unusual activity
  - [GRPC_METHOD_NAME errors, last hour](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20hostname:dev%20AND%20NOT%20grpc.code:OK%20AND%20message:finished'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-1h,mode:quick,to:now%29%29) for unusual activity
- [ ] **Check for errors** in [Gitaly Dev Sentry](https://sentry.gitlap.com/gitlab/devgitlaborg-gitaly/?query=is%3Aunresolved+grpc.method%3A%2Fgitaly.GRPC_SERVICE_NAME%2FGRPC_METHOD_NAME)
- [ ] **Check for errors** in [GitLab Dev Sentry](https://sentry.gitlap.com/gitlab/devgitlaborg/?query=is%3Aunresolved+gitaly)

#### Continue?

- [ ] On unexpectedly high calls rates, error rates, CPU activity, etc, disable trial immediately with `!feature-set GITALY_FEATURE_NAME false` in [`#dev-gitlab`](https://gitlab.slack.com/messages/C6WQ87MU3) otherwise leave running and proceed proceed to next stage.

## 3. Staging Trial

#### Check Staging Server Versions
- [ ] Gitaly: [Gitaly Staging Version Tracker Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-version-tracker?orgId=1&var-job=gitaly-staging)
- [ ] GitLab: https://staging.gitlab.com/help

#### Enable on `staging.gitlab.com`
- [ ] `!feature-set GITALY_FEATURE_NAME true` in [`#development`](https://gitlab.slack.com/messages/C02PF508L/)

Then leave running while monitoring for at least **15 minutes** while performing some testing through web, api or SSH.

#### Monitor (at least every 5 minutes, preferably real-time)

- [ ] **Monitor Grafana** feature dashboard on staging: [Gitaly Feature Status Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-feature-status?from=now-12h&to=now&orgId=1&var-method=GRPC_METHOD_NAME&var-job=gitaly-nfs-staging&refresh=5m)
- [ ] **Inspect logs** in ELK:
  - [GRPC_METHOD_NAME invocations, last hour](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20hostname:nfs5'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-1h,mode:quick,to:now%29%29) for unusual activity
  - [GRPC_METHOD_NAME errors, last hour](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20hostname:nfs5%20AND%20NOT%20grpc.code:OK%20AND%20message:finished'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-1h,mode:quick,to:now%29%29) for unusual activity
- [ ] **Check for errors** in [Gitaly Staging Sentry](https://sentry.gitlap.com/gitlab/staginggitlabcom-gitaly/?query=is%3Aunresolved+grpc.method%3A%2Fgitaly.GRPC_SERVICE_NAME%2FGRPC_METHOD_NAME)
- [ ] **Check for errors** in [GitLab Staging Sentry](https://sentry.gitlap.com/gitlab/staginggitlabcom/?query=is%3Aunresolved+gitaly)

#### Continue?

- [ ] On unexpectedly high calls rates, error rates, CPU activity, etc, disable trial immediately using `!feature-set GITALY_FEATURE_NAME false` in [`#development`](https://gitlab.slack.com/messages/C02PF508L/) otherwise leave running and proceed to next stage.

## 4. Production Server Version Check

- [ ] Gitaly: [Gitaly Production Version Tracker Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-version-tracker?orgId=1&var-job=gitaly-production)
- [ ] GitLab: https://gitlab.com/help

## 5. Initial Impact Check

- [ ] Create an issue in the infrastructure tracker: [Create issue now](https://gitlab.com/gitlab-com/infrastructure/issues/new?issue[title]=Testing%20of%20Gitaly%20Feature%20GITALY_FEATURE_NAME&issue[description]=https%3A%2F%2Fgitlab.com%2Fgitlab-org%2Fgitaly%2Fissues%2FACCEPTANCE_TEST_ISSUE_NUMBER%0A%0A%2Flabel%20~gitaly%20~change)
- [ ] Set Gitaly to 1% using the command `!feature-set GITALY_FEATURE_NAME 1` in [`#production`](https://gitlab.slack.com/messages/C101F3796/)

Then leave running while monitoring for at least **15 minutes** while performing some testing through web, api or SSH.

#### Monitor (at least every 5 minutes, preferably real-time)
- [ ] **Monitor Grafana** feature dashboard on production: [Gitaly Feature Status Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-feature-status?from=now-12h&to=now&orgId=1&var-method=GRPC_METHOD_NAME&var-job=gitaly-production&refresh=5m)
- [ ] **Inspect logs** in ELK:
  - [GRPC_METHOD_NAME invocations, last hour](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20NOT%20hostname:dev'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-1h,mode:quick,to:now%29%29) for unusual activity
  - [GRPC_METHOD_NAME errors, last hour](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20NOT%20hostname:dev%20AND%20NOT%20grpc.code:OK%20AND%20message:finished'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-1h,mode:quick,to:now%29%29) for unusual activity
- [ ] **Check for errors** in [Gitaly Sentry](https://sentry.gitlap.com/gitlab/gitaly-production/?query=is%3Aunresolved+grpc.method%3A%2Fgitaly.GRPC_SERVICE_NAME%2FGRPC_METHOD_NAME)
- [ ] **Check for errors** in [GitLab Sentry](https://sentry.gitlap.com/gitlab/gitlabcom/?query=is%3Aunresolved+gitaly)

#### Continue?

- [ ] On unexpectedly high calls rates, error rates, CPU activity, etc, disable trial immediately with `!feature-set GITALY_FEATURE_NAME false` in [`#production`](https://gitlab.slack.com/messages/C101F3796/) otherwise leave running and proceed to next stage.

## 6. Low Impact Trial

- [ ] Set Gitaly to 5% using the command `!feature-set GITALY_FEATURE_NAME 5` in [`#production`](https://gitlab.slack.com/messages/C101F3796/)

Then leave running while monitoring for at least **2 hours**.

#### Monitor (at least every 20 minutes)
- [ ] **Monitor Grafana** feature dashboard on production: [Gitaly Feature Status Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-feature-status?from=now-12h&to=now&orgId=1&var-method=GRPC_METHOD_NAME&var-job=gitaly-production&refresh=5m)
- [ ] **Inspect logs** in ELK:
  - [GRPC_METHOD_NAME invocations, last 2 hours](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20NOT%20hostname:dev'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-2h,mode:quick,to:now%29%29) for unusual activity
  - [GRPC_METHOD_NAME errors, last 2 hours](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20NOT%20hostname:dev%20AND%20NOT%20grpc.code:OK%20AND%20message:finished'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-2h,mode:quick,to:now%29%29) for unusual activity
- [ ] **Check for errors** in [Gitaly Sentry](https://sentry.gitlap.com/gitlab/gitaly-production/?query=is%3Aunresolved+grpc.method%3A%2Fgitaly.GRPC_SERVICE_NAME%2FGRPC_METHOD_NAME)
- [ ] **Check for errors** in [GitLab Sentry](https://sentry.gitlap.com/gitlab/gitlabcom/?query=is%3Aunresolved+gitaly)

#### Continue?

- [ ] On unexpectedly high calls rates, error rates, CPU activity, etc, disable trial immediately with `!feature-set GITALY_FEATURE_NAME false` in [`#production`](https://gitlab.slack.com/messages/C101F3796/) otherwise leave running and proceed to next stage.

## 7. Mid Impact Trial

- [ ] Set Gitaly to 50% using the command `!feature-set GITALY_FEATURE_NAME 50` in [`#production`](https://gitlab.slack.com/messages/C101F3796/)

Then leave running while monitoring for at least **24 hours**.

#### Monitor (at least every few hours)
- [ ] **Monitor Grafana** feature dashboard on production: [Gitaly Feature Status Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-feature-status?from=now-12h&to=now&orgId=1&var-method=GRPC_METHOD_NAME&var-job=gitaly-production&refresh=5m)
- [ ] **Inspect logs** in ELK:
  - [GRPC_METHOD_NAME invocations, last 24 hours](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20NOT%20hostname:dev'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-24h,mode:quick,to:now%29%29) for unusual activity
  - [GRPC_METHOD_NAME errors, last 24 hours](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20NOT%20hostname:dev%20AND%20NOT%20grpc.code:OK%20AND%20message:finished'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-24h,mode:quick,to:now%29%29) for unusual activity
- [ ] **Check for errors** in [Gitaly Sentry](https://sentry.gitlap.com/gitlab/gitaly-production/?query=is%3Aunresolved+grpc.method%3A%2Fgitaly.GRPC_SERVICE_NAME%2FGRPC_METHOD_NAME)
- [ ] **Check for errors** in [GitLab Sentry](https://sentry.gitlap.com/gitlab/gitlabcom/?query=is%3Aunresolved+gitaly)

#### Continue?

- [ ] On unexpectedly high calls rates, error rates, CPU activity, etc, disable trial immediately with `!feature-set GITALY_FEATURE_NAME false` in [`#production`](https://gitlab.slack.com/messages/C101F3796/) otherwise leave running and proceed to next stage.

## 8. Full Impact Trial

- [ ] Set Gitaly to 100% using the command `!feature-set GITALY_FEATURE_NAME 100` in [`#production`](https://gitlab.slack.com/messages/C101F3796/)

Then leave running while monitoring for at least **1 week**.

#### Monitor (at least every day)
- [ ] **Monitor Grafana** feature dashboard on production: [Gitaly Feature Status Dashboard](https://performance.gitlab.net/dashboard/db/gitaly-feature-status?from=now-12h&to=now&orgId=1&var-method=GRPC_METHOD_NAME&var-job=gitaly-production&refresh=5m)
- [ ] **Inspect logs** in ELK:
  - [GRPC_METHOD_NAME invocations, last 7 days](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20NOT%20hostname:dev'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-7d,mode:quick,to:now%29%29) for unusual activity
  - [GRPC_METHOD_NAME errors, last 7 days](https://log.gitlap.com/app/kibana#/discover?_a=%28index%3A'gitaly-*'%2Cquery%3A%28query_string%3A%28query%3A'grpc.method:GRPC_METHOD_NAME%20AND%20NOT%20hostname:dev%20AND%20NOT%20grpc.code:OK%20AND%20message:finished'%29%29%29&_g=%28refreshInterval:%28display:Off,pause:!f,value:0%29,time:%28from:now-7d,mode:quick,to:now%29%29) for unusual activity
- [ ] **Check for errors** in [Gitaly Sentry](https://sentry.gitlap.com/gitlab/gitaly-production/?query=is%3Aunresolved+grpc.method%3A%2Fgitaly.GRPC_SERVICE_NAME%2FGRPC_METHOD_NAME)
- [ ] **Check for errors** in [GitLab Sentry](https://sentry.gitlap.com/gitlab/gitlabcom/?query=is%3Aunresolved+gitaly)

#### Success?

- [ ] Close this issue and mark the ~Conversation as ~"Migration:Opt-In"

/label ~"Acceptance Testing"
