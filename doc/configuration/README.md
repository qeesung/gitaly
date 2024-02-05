# Configuring Gitaly

This document describes how to configure the Gitaly server
application.

Gitaly is configured via a [TOML](https://github.com/toml-lang/toml)
configuration file. Where this TOML file is located and how you should
edit it depend on how you installed GitLab. See:
<https://docs.gitlab.com/ee/administration/gitaly/>

The configuration file is passed as an argument to the `gitaly`
executable. This is usually done by either `omnibus-gitlab` or your init
script.

```shell
gitaly /path/to/config.toml
```

## Format

```toml
socket_path = "/path/to/gitaly.sock"
listen_addr = ":8081"
bin_dir = "/path/to/gitaly-executables"
prometheus_listen_addr = ":9236"

[auth]
# transitioning = false
# token = "abc123def456......."

[[storage]]
path = "/path/to/storage/repositories"
name = "my_shard"

# Gitaly may serve from multiple storages
#[[storage]]
#name = "other_storage"
#path = "/path/to/other/repositories"
```

| Name                     | Type   | Required  | Notes                                                                                               |
|:-------------------------|:-------|:----------|:----------------------------------------------------------------------------------------------------|
| `socket_path`            | string | see notes | A path which Gitaly should open a Unix socket. Required unless listen_addr is set                   |
| `listen_addr`            | string | see notes | TCP address for Gitaly to listen on. Required unless `socket_path` is set |
| `internal_socket_dir`    | string | yes       | Path where Gitaly will create sockets for internal Gitaly calls to connect to                       |
| `bin_dir`                | string | yes       | Directory containing Gitaly's executables                                                           |
| `prometheus_listen_addr` | string | no        | TCP listen address for Prometheus metrics. If not set, no Prometheus listener is started            |
| `storage`                | array  | yes       | An array of storage shards                                                                          |

### Authentication

Gitaly can be configured to reject requests that do not contain a
specific bearer token in their headers. This is a security measure to
be used when serving requests over TCP.

Authentication is disabled when the token setting in `config.toml` is absent or the empty string.

```toml
[auth]
# Non-empty token: this enables authentication.
token = "the secret token"
```

It is possible to temporarily disable authentication with the 'transitioning'
setting. This allows you to monitor (see below) if all clients are
authenticating correctly without causing a service outage for clients
that are not configured correctly yet.

> **Warning:** Remember to disable 'transitioning' when you are done
changing your token settings.

```toml
[auth]
token = "the secret token"
transitioning = true
```

All authentication attempts are counted in Prometheus under
the `gitaly_authentications_total` metric.

### Storage

GitLab repositories are grouped into 'storages'. These are directories
(e.g. `/home/git/repositories`) containing bare repositories managed
by GitLab , with names (e.g. `default`).

These names and paths are also defined in the `gitlab.yml`
configuration file of `gitlab-foss` (or `gitlab`). When you run Gitaly on
the same machine as `gitlab-foss`, which is the default and recommended
configuration, storage paths defined in Gitaly's `config.toml` must
match those in `gitlab.yml`.

| Name   | Type   | Required | Notes                 |
|:-------|:-------|:---------|:----------------------|
| `path` | string | yes      | Path to storage shard |
| `name` | string | yes      | Name of storage shard |

### Git

The following values can be set in the `[git]` section of the configuration file:

| Name                 | Type    | Required | Notes                                                                 |
|:---------------------|:--------|:---------|:----------------------------------------------------------------------|
| `bin_path`           | string  | no       | Path to Git binary. If not set, will be resolved using PATH.          |
| `catfile_cache_size` | integer | no       | Maximum number of cached cat-file processes (see below). Default 100. |

#### cat-file cache

A lot of Gitaly RPC's need to look up Git objects from repositories.
Most of the time we use `git cat-file --batch` processes for that. For
the sake of performance, Gitaly can re-use thse `git cat-file` processes
across RPC calls. Previously used processes are kept around in a "Git
cat-file cache". In order to control how much system resources this uses
we have a maximum number of cat-file processes that can go into the
cache.

The default limit is 100 "catfiles", which constitute a pair of
`git cat-file --batch` and `git cat-file --batch-check` processes. If
you are seeing errors complaining about "too many open files", or an
inability to create new processes, you may want to lower this limit.

Ideally the number should be large enough to handle normal (peak)
traffic. If you raise the limit you should measure the cache hit ratio
before and after. If the hit ratio does not improve, the higher limit is
probably not making a meaningful difference. Here is an example
Prometheus query to see the hit rate:

```prometheus
sum(rate(gitaly_catfile_cache_total{type="hit"}[5m])) / sum(rate(gitaly_catfile_cache_total{type=~"(hit)|(miss)"}[5m]))
```

### Logging

Example:

```toml
[logging]
level = "warn"
```

| Name                 | Type   | Required | Notes                                                                             |
|:---------------------|:-------|:---------|:----------------------------------------------------------------------------------|
| `format`             | string | no       | Log format: `text` or `json`. Default: `text`                                     |
| `level`              | string | no       | Log level: `debug`, `info`, `warn`, `error`, `fatal`, or `panic`. Default: `info` |
| `sentry_dsn`         | string | no       | Sentry DSN for exception monitoring                                               |
| `sentry_environment` | string | no       | Sentry Environment for exception monitoring                                       |

#### Environment variables

| Name                                      | Default                          | Notes                                                                      |
|:------------------------------------------|:---------------------------------|:---------------------------------------------------------------------------|
| `GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN` |                                  | Regular expression that controls which gRPC methods should be logged       |
| `GITALY_LOG_REQUEST_METHOD_DENY_PATTERN`  | `^/grpc.health.v1.Health/Check$` | Regular expression that controls which gRPC methods should be filtered out |

Note that `GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN` takes precedence
over `GITALY_LOG_REQUEST_METHOD_DENY_PATTERN`. If a pattern matches in
allow pattern, then it will be logged, even if it also matches the deny
pattern. All error messages are logged unconditionally.

By default, health check gRPC messages are not logged. To enable them,
set `GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN` to `.`.

### Validate Gitaly configuration

To validate Gitaly configuration, use `gitaly configuration validate`. For example:

```shell
gitaly configuration validate < gitaly.config.toml
```

For more information, run:

```shell
gitaly configuration validate --help
```
