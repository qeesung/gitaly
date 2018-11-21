# gitaly-debug

Gitaly-debug provides "production debugging" tools for Gitaly and Git
performance. It is intended to help production engineers and support
engineers investigate Gitaly performance problems.

## Installation

If you're using GitLab 11.6 or newer this tool should be installed on
your GitLab / Gitaly server already at
`/opt/gitlab/embedded/bin/gitaly-debug`.

If you're investigating an older GitLab version you can compile this
tool offline and copy the executable to your server.

    GOOS=linux GOARCH=amd64 go build -o gitaly-debug

## Subcommands

### `simulate-http-clone`

    gitaly-debug simulate-http-clone /path/to/repo.git

This simulates the server workload for a full HTTP Git clone on a
repository on your Gitaly server. You can use this to determine the
best-case performance of `git clone`. An example application is to
determine if a slow `git clone` is bottle-necked by Gitaly server
performance, or by something downstream.

The results returned by this command give an indication of the ideal
case performance of a `git clone` on the repository, as if there is
unlimited network bandwidth and no latency. This speed will not be
reached in real life but it shows the best you can hope for from a given
repository on a given Gitaly server.
