## Complete guide to Gitaly contributions

### Setup

#### GitLab

Before you can develop on Gitaly, its required to have a
[GitLab Development Kit][gdk] properly installed. After installing GitLab, verify
it to be working by starting the required servers and visiting GitLab on
`http://localhost:3000`.

#### Go

For the GitLab development Kit to run, both Ruby and Golang are installed.
The last step required for golang development, is the setup of a workspace.
Please consult the [official documentation][go-workspace] on this topic.

#### Gitaly Proto

GitLab will want to read and manipulate git data, to do this it needs to talk
to Gitaly. For GitLab and Gitaly its important to have a set protocol. This
protocol defines what requests can be made and what data the requester has to
send with the request. For each request the response is defined too.

To define new requests/responses, or modify existing behaviour the project
needs to be present on your machine. Sign into [GitLab.com][gitlab] and
fork the [Gitaly Proto project][gitaly-proto]. Afterward your can run:

```bash
# Create the needed directory
$ mkdir -p $GOPATH/src/gitlab.com/<your-username>
$ cd $GOPATH/src/gitlab.com/<your-username>
$ git clone https://gitlab.com/<your-username/gitaly-proto.git
```
#### Gitaly

Gitaly is a component that calls procedure on the git data when its requested
to do so. Gitaly is bundled in the [GDK][gdk], but for development purposes
another copy can be stored in the `$GOPATH`. First you can fork the
[Gitaly project][gitaly] project. Than you can run:

```bash
$ cd $GOPATH/src/gitlab.com/<your-username>
$ git clone gitlab.com/<your-username>/gitaly.git
```

To verify your install, please change your directory to
`$GOPATH/src/gitlab.com/<your-username>/gitaly` and run `make`. And afterwards
`make test`. Again, if any errors occur, please [open an issue][gitaly-issue].

### Development

#### Process

In general there are a couple of stages to go through, in order:
1. Add a request/response combination to [Gitaly Proto][gitaly-proto], or edit
  an existing one
1. Change [Gitaly][gitaly] accourdingly
1. Use the endpoint in other GitLab components (CE/EE, GitLab Workhorse, etc.)

##### Gitaly Proto

The [Protocol buffer documentation][proto-docs] combined with the `*.proto` files
should be enough to get you started. A service needs to be picked that can
receive the procedure call. A general rule of thumb is that the service is named
either after the git cli command, or after the git object type.

If either your request of response data will exceed 1MB you need to use the
`stream` keyword. To generate the server and client code, run `make`. If this
succeeds without any errors, create a feature branch to commit your changes to.
Than create a merge request and wait for a review.

##### Gitaly

If the proto changes are merged and released in a new version, the Gitaly server
can be expanded to handle the new request. First update the version for
`gitaly-proto`:

```bash
# 0.71.0 is the version number in this example
$ govendor fetch gitlab.com/gitlab-org/gitaly-proto/go@=v0.71.0`

# change the versions in Gemfile for gitaly-proto
$ cd ruby
$ bundle
```

If proto is updated, run `make`. This will fail to compile Gitaly, as Gitaly
doesn't yet have the new endpoint implemented.

###### Go boilerplate

To create the Ruby endpoint, some go is required as the go code receives the
requests and reroutes it to the go server. In general this is boilerplate code
where only method- and variable names are different.

Examples:
- Simple: [Simple request in, simple response out](https://gitlab.com/gitlab-org/gitaly/blob/6841327adea214666417ee339ca37b58b20c649c/internal/service/wiki/delete_page.go)
- Client Streamed: [Stream in, simple response out](https://gitlab.com/gitlab-org/gitaly/blob/6841327adea214666417ee339ca37b58b20c649c/internal/service/wiki/write_page.go)
- Server Streamed: [Simple request in, streamed response out](https://gitlab.com/gitlab-org/gitaly/blob/6841327adea214666417ee339ca37b58b20c649c/internal/service/wiki/find_page.go)
- Bidirectional: No example at this time

###### Ruby

The ruby code needs to added to `ruby/lib/gitaly_server/<service-name>_service.rb`.
The method name should match the name defined by the `gitaly-proto` gem. To be sure
run `bundle open gitaly-proto`. The return value of the method should be an
instance of the response object.

### Testing

Tests can be written in Ruby with Rspec. These can be found in `ruby/spec/`. These tests are
end to end tests, so the Go code is tested too.

[gdk]: https://gitlab.com/gitlab-org/gitlab-development-kit/#getting-started
[git-remote]: https://git-scm.com/book/en/v2/Git-Basics-Working-with-Remotes
[gitaly]: https://gitlab.com/gitlab-org/gitaly
[gitaly]: https://gitlab.com/gitlab-org/gitaly/issues
[gitaly-proto]: https://gitlab.com/gitlab-org/gitaly-proto
[gitaly-proto-issue]: https://gitlab.com/gitlab-org/gitaly-proto/issues
[gitlab]: https://gitlab.com
[go-workspace]: https://golang.org/doc/code.html#Workspaces
[proto-docs]: https://developers.google.com/protocol-buffers/docs/overview
