# gitaly/client config change checklist

When you make a change in gitlab.com/gitlab-org/gitaly/client you need to perform a number of steps to actually ship the change.

- [ ] update [go.mod in gitlab-shell](https://gitlab.com/gitlab-org/gitlab-shell/-/blob/master/go.mod) MR LINK
- [ ] update [go.mod in gitlab-workhorse](https://gitlab.com/gitlab-org/gitlab-workhorse/-/blob/master/go.mod) MR LINK
- [ ] update [go.mod in gitlab-elasticsearch-indexer](https://gitlab.com/gitlab-org/gitlab-elasticsearch-indexer/-/blob/master/go.mod) MR LINK
- [ ] wait/ask for gitlab-shell release TAG LINK
- [ ] wait/ask for gitlab-workhorse release TAG LINK
- [ ] wait/ask for gitlab-elasticsearch-indexer release TAG LINK
- [ ] update [GITLAB_SHELL_VERSION in gitlab](https://gitlab.com/gitlab-org/gitlab/-/blob/master/GITLAB_SHELL_VERSION) MR LINK
- [ ] update [GITLAB_WORKHORSE_VERSION in gitlab](https://gitlab.com/gitlab-org/gitlab/-/blob/master/GITLAB_WORKHORSE_VERSION) MR LINK
- [ ] update [GITLAB_ELASTICSEARCH_INDEXER_VERSION in gitlab](https://gitlab.com/gitlab-org/gitlab/-/blob/master/GITLAB_ELASTICSEARCH_INDEXER_VERSION) MR LINK

