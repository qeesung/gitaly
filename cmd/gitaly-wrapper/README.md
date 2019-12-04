# gitaly-wrapper

## How is it invoked?

```sh
GITALY_PID_FILE=/tmp/path/to/pid/file gitaly-wrapper <gitaly binary>|<praefect binary> [arguments]...
```

example usage with gitaly:

```sh
GITALY_PID_FILE=/var/opt/gitlab/gitaly/gitaly.pid /opt/gitlab/embedded/bin/gitaly-wrapper /opt/gitlab/embedded/bin/gitaly /var/opt/gitlab/gitaly/config.toml
```

example usage with praefect:

```sh
GITALY_PID_FILE=/var/opt/gitlab/praefect/praefect.pid /opt/gitlab/embedded/bin/gitaly-wrapper /opt/gitlab/embedded/bin/praefect -config /var/opt/gitlab/praefect/config.toml
```

`GITALY_PID_FILE` provides `gitaly-wrapper` with the location of the pid file it will look at to find the current running process.

The first argument to gitaly-wrapper must be either the path to the gitaly binary or the path to the praefect binary.

## Why is it needed?

Both Gitaly and Praefect are integrated with [Cloudflare's Tableflip Library](https://github.com/cloudflare/tableflip), which handles zero downtime upgrades and restarts. Each time Gitaly or Praefect is sent a HUP signal, tableflip will automatically start a new process on a new PID, and gracefully forwards new connections to the new running process.

However, [runit](http://smarden.org/runit/), which omnibus uses to manage daemons, cannot handle daemons with changing PIDs. `gitaly-wrapper` allows runit to play well with the tableflip library by allowing runit to ignore the changing PIDs of Gitaly/Praefect, and only have to know about the `gitaly-wrapper` process PID, while `gitaly-wrapper` finds the underlying Gitaly or Praefect process.

## What does it do?

`gitaly-wrapper` tries to find the process of the PID in the file `GITALY_PID_FILE` points to.  If this process is alive and matches the name of the binary, it will adopt this process. If not, it will spawn a new process with `GITALY_UPGRADES_ENABLED=true`.

