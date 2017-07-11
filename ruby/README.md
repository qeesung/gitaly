# `gitaly-ruby`

`gitaly-ruby` is a 'sidecar' process for the main Gitaly service. It
allows us to run legacy Ruby application code for which it would be
too risky or even infeasible to port it to Go. It will also speed up
the Gitaly migration project.

## Architecture

Gitaly-ruby is a minimal Ruby gRPC service which should only receive
requests from its (Go) parent Gitaly process. The Gitaly parent
handles authentication, logging, metrics, configuration file parsing
etc.

The Gitaly parent is also responsible for starting and (if necessary)
restarting Gitaly-ruby.

## Authentication

Gitaly-ruby listens on a Unix socket in a temporary directory with
mode 0700. It runs as the same user as the Gitaly parent process.

## Testing

All tests for code in Gitaly-ruby go through the parent Gitaly process
for two reasons. Firstly, testing through the parent proves that the
Ruby code under test is reachable. Secondly, testing through the
parent will make it easier to create a Go implementation in the parent
if we ever want to do that.
