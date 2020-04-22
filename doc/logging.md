# Logging in Gitaly

Gitaly creates several kinds of log data.

## Go application logs

The main Gitaly process uses logrus to writes structured logs to
stdout. These logs use either the text or the json format of logrus,
depending on setting in the Gitaly config file.

The main Gitaly process writes log messages with global scope and
request scope. Request scoped messages can be recognized and filtered
by their correlation ID.

The main application logs include an access log for all requests.
Request-scoped errors are printed with the request correlation ID
attached.

Many Gitaly RPC's spawn Git processes which may write errors or
warnings to stderr. Gitaly will capture these stderr messages and
include them in its main log, tagged with the request correlation ID.

## Gitaly-ruby application logs

Gitaly-ruby writes logs to stdout. These logs are not structured. The
main Gitaly process captures the gitaly-ruby process log messages and
converts each line into a structured message that includes information
about the gitaly-ruby process such as the PID. These logs then get
printed as part of the log stream of the main Gitaly process.

There is no attribution of log messages in gitaly-ruby beyond the
gitaly-ruby process ID. If an RPC implemented in gitaly-ruby runs a
Git command, and if that Git command prints to stderr, it will show up
as untagged data in the log stream for the gitaly-ruby parent process.

Because of these properties, gitaly-ruby logs are often hard to read,
and it is often not possible to attribute log messages to individual
RPC requests.

## Log files

In a few cases, Gitaly spawns process that cannot log to stderr
because stderr gets relayed to the end user, and we would risk leaking
information about the machine Gitaly runs on. One (the only?) example
is Git hooks. Because of this, the Gitaly config file also has a log
directory setting. Hooks that must log to a file will use that
directory.

Examples are:

- `gitlab-shell.log`
- `gitaly_hooks.log`

There is another log file called `githost.log`. This log is generated
by legacy code in gitaly-ruby. The way it is used, it might as well
write to stdout.
