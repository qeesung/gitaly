## Beginner's guide to Gitaly contributions

### Setup

#### GitLab

Before you can develop on Gitaly, it's required to have a
[GitLab Development Kit][gdk] properly installed. After installing GitLab, verify
it to be working by starting the required servers and visiting GitLab on
`http://localhost:3000`.

#### Gitaly Proto

GitLab will want to read and manipulate Git data, to do this it needs to talk
to Gitaly. For GitLab and Gitaly it's important to have a set protocol. This
protocol defines what requests can be made and what data the requester has to
send with the request. For each request the response is defined too.

The protocol definitions can be found in `proto/*.proto`.

#### Gitaly

Gitaly is a component that calls procedure on the Git data when it's requested
to do so.

You can find a clone of the gitaly repository in `/path/to/gdk/gitaly/src/gitlab.com/gitlab-org/gitaly`.
You can check out your working branch here, but be aware that `gdk update` will reset it to the tag specified by `/path/to/gdk/gitlab/GITALY_SERVER_VERSION`.

##### GOPATH and GDK

> If you don't know what GOPATH is you can skip this section.

Although you can compile and test Gitaly using `make` without having to set a
GOPATH, you often want to do your Go development work in the context
of a GOPATH. At the same time, when working on GitLab, it is useful to
use GDK so that you can see e.g. how your Gitaly contribution
integrates into GitLab as a whole.

You have the following options to combine GOPATH and GDK:

1. (default and recommended) use the embedded Gitaly GOPATH in GDK
2. (if you like having a global GOPATH) run `go get
   gitlab.com/gitlab-org/gitaly` in your global GOPATH, and symlink
   `/path/to/gdk/gitaly/src/gitlab.com/gitlab-org/gitaly` to
   `$GOPATH/src/gitlab.com/gitlab-org/gitaly`

Option 1 is set up automatically for you when you install GDK. It gives
you several options:

- use a "dumb" text editor and run `go` commands in a dedicated
  terminal window with the correct exported GOPATH
- use VSCode with `"go.inferGopath": true` in your user settings. This
  will auto-detect the GDK embededded GOPATH
- launch a new Atom window with the GDK embedded GOPATH:

```
cd /path/to/gdk/gitaly/src/gitlab.com/gitlab-org/gitaly
GOPATH=/path/to/gdk/gitaly atom .
```

About option 2: most editors with Go language integration should have a
way to let you set GOPATH for a specific editor session. If you want
to use a global GOPATH anyway, you can do the following to still get
GDK integration:

```
cd /path/to/gdk/gitaly/src/gitlab.com/gitlab-org/
mv gitaly gitaly.deleted
ln -s /path/to/global/gopath/src/gitlab.com/gitlab-org/gitaly gitaly
```

Now you can open the copy of Gitaly inside your global GOPATH with
your favourite editor, and all your plugins should work. Note that when
`/path/to/gdk/gitaly/src/gitlab.com/gitlab-org/gitaly` is a symlink,
some tools break. In this case run commands in your global GOPATH instead.

### Development

#### General advice

##### Editing code and seeing what happens

If you're used to Ruby on Rails development you may be used to a "edit
code and reload" cycle where you keep editing and reloading until you
have your change working. This is not a good workflow for Gitaly
development.

At some point you will know which Gitaly RPC you need to work on. This
is where you probably want to stop using `localhost:3000` and zoom in on
the RPC instead.

To experiment with changing an RPC you should use the Gitaly service
tests. The RPC you want to work on will have tests somewhere in
`internal/service/...`. Find the tests for your RPC. Next, before you
edit any code, make sure the tests pass when you run them:
`go test ./internal/service/foobar -count 1 -run MyRPC`. In this
command, `MyRPC` is a regex that will match functions like
`TestMyRPCSuccess` and `TestMyRPCValidationFailure`. Once you have found
your tests and your test command, you can start tweaking the
implementation or adding test cases and re-running the tests. The cycle
is "edit code, run tests".

This is many times faster than "edit gitaly, reinstall Gitaly into GDK,
restart, reload localhost:3000".

#### Process

In general there are a couple of stages to go through, in order:
1. Add a request/response combination to [Gitaly Proto][gitaly-proto], or edit
  an existing one
1. Change [Gitaly][gitaly] accordingly
1. Use the endpoint in other GitLab components (CE/EE, GitLab Workhorse, etc.)

##### Gitaly Proto

The [Protocol buffer documentation][proto-docs] combined with the
`*.proto` files in the `proto/` directory should
be enough to get you started. A service needs to be picked that can
receive the procedure call. A general rule of thumb is that the
service is named either after the Git CLI command, or after the Git
object type.

If either your request or response data can exceed 100KB you need to use the
`stream` keyword. To generate the server and client code, run `make proto`.

##### Gitaly

If proto is updated, run `make`. This will fail to compile Gitaly, as Gitaly
doesn't yet have the new endpoint implemented. We fix this by adding a dummy implementation.

###### Adding an empty Go implementation for a new RPC

Often the other developers add a new protocol to gitaly/proto and it could interfere your merge request
due to the lack of corresponding implementations in gitaly. For instance, you'd see the following
Go compiler error in such case:

```
# gitlab.com/gitlab-org/gitaly/internal/service/repository
_build/src/gitlab.com/gitlab-org/gitaly/internal/service/repository/server.go:15:17: cannot use server literal (type *server) as type gitaly.RepositoryServiceServer in return argument:
    *server does not implement gitaly.RepositoryServiceServer (missing RestoreCustomHooks method)
```

Remember that this sort of error can be addressed by the following method
(even if you're not the one who added the protocol).

In this case a new RPC called `RestoreCustomHooks` was added to the
RepositoryService service, but it does not have an implementation. We
fix this by adding a dummy implementation.

Open the file that triggered the error, in this case
`internal/service/repository/server.go`. We add a (wrong) dummy
function so that we can get hints from the compiler:

```
func (*server) RestoreCustomHooks() {
}
```

When we run `make` again, we now get a different error.

```
# gitlab.com/gitlab-org/gitaly/internal/service/repository
_build/src/gitlab.com/gitlab-org/gitaly/internal/service/repository/server.go:15:17: cannot use server literal (type *server) as type gitalypb.RepositoryServiceServer in return argument:
	*server does not implement gitalypb.RepositoryServiceServer (wrong type for RestoreCustomHooks method)
		have RestoreCustomHooks()
		want RestoreCustomHooks(gitalypb.RepositoryService_RestoreCustomHooksServer) error
```

This error tells us the expected signature. We copy-paste this
signature into server.go.

```
func (*server) RestoreCustomHooks(gitalypb.RepositoryService_RestoreCustomHooksServer) error {
}
```

Run `make` again, now we get:

```
# gitlab.com/gitlab-org/gitaly/internal/service/repository
_build/src/gitlab.com/gitlab-org/gitaly/internal/service/repository/server.go:19:1: missing return at end of function
```

The final solution is to add
`"gitlab.com/gitlab-org/gitaly/internal/helper"` to the imports in
server.go, and make the function:

```
func (*server) RestoreCustomHooks(gitalypb.RepositoryService_RestoreCustomHooksServer) error {
	return helper.Unimplemented
}
```

Note that the exact signature and the number of arguments / response
values depends on the RPC type. Use the hints from the compiler to get
the correct dummy function.

##### Gitaly-ruby

Gitaly is mostly written in Go but it also uses a pool of Ruby helper
processes. This helper application is called gitaly-ruby and its code
is in the `ruby` subdirectory of Gitaly. Gitaly-ruby is a gRPC server,
just like its Go parent process. The Go parent proxies certain
requests to gitaly-ruby.

Currently (GitLab 10.8) it is our experience that gitaly-ruby is
unsuitable for RPC's that are slow, or that are called at a high rate.
It should only be used for:

- legacy GitLab application code that is too complex or subtle to rewrite in Go
- prototyping (if you the contributor are uncomfortable writing Go)

Note that for any changes to `gitaly-ruby` to be used by GDK, you need to
run `make gitaly-setup` in your GDK root and restart your processes.

###### Gitaly-ruby boilerplate

To create the Ruby endpoint, some Go is required as the go code receives the
requests and proxies it to the Go server. In general this is boilerplate code
where only method and variable names are different.

Examples:
- Simple: [Simple request in, simple response out](https://gitlab.com/gitlab-org/gitaly/blob/6841327adea214666417ee339ca37b58b20c649c/internal/service/wiki/delete_page.go)
- Client Streamed: [Stream in, simple response out](https://gitlab.com/gitlab-org/gitaly/blob/6841327adea214666417ee339ca37b58b20c649c/internal/service/wiki/write_page.go)
- Server Streamed: [Simple request in, streamed response out](https://gitlab.com/gitlab-org/gitaly/blob/6841327adea214666417ee339ca37b58b20c649c/internal/service/wiki/find_page.go)
- Bidirectional: No example at this time

###### Ruby

The Ruby code needs to be added to `ruby/lib/gitaly_server/<service-name>_service.rb`.
The method name should match the name defined by the `gitaly` gem. To be sure
run `bundle open gitaly`. The return value of the method should be an
instance of the response object.

There is no autoloader in gitaly-ruby. If you add new ruby files, you need to manually
add a `require` statement in `ruby/lib/gitlab/git.rb` or `ruby/lib/gitaly_server.rb.`

### Testing

Gitaly's tests are mostly written in Go but it is possible to write RSpec tests too.

Generally, you should always write new tests in Go even when testing Ruby code,
since we're planning to gradually rewrite everything in Go and want to avoid
having to rewrite the tests as well.

To run the full test suite, use `make test`.
You'll need some [test repositories](test_repos.md), you can set these up with `make prepare-tests`.

#### Go tests

- each RPC must have end-to-end tests at the service level
- optionally, you can add unit tests for functions that need more coverage

A typical set of Go tests for an RPC consists of two or three test
functions: a success test, a failure test (usually a table driven test
using t.Run), and sometimes a validation test. Our Go RPC tests use
in-process test servers that only implement the service the current
RPC belongs to. So if you are working on an RPC in the
'RepositoryService', your tests would go in
`internal/service/repository/your_rpc_test.go`.

##### Running one specific Go test

When you are trying to fix a specific test failure it is inefficient
to run `make test` all the time. To run just one test you need to know
the package it lives in (e.g. `internal/service/repository`) and the
test name (e.g. `TestRepositoryExists`).

To run the test you need a terminal window with working directory
`/path/to/gdk/gitaly/src/gitlab.com/gitlab-org/gitaly`. Make sure
GOPATH is set correctly in your terminal window:

```
export GOPATH=/path/to/gdk/gitaly
```

Now you can run the one test you're interested in:

```
go test -count 1 -run TestRepositoryExists ./internal/service/repository
```

When writing tests, prefer using [testify]'s [require], and [assert] as
methods to set expectations over functions like `t.Fatal()` and others directly
called on `testing.T`.

[testify]: https://github.com/stretchr/testify
[require]: https://github.com/stretchr/testify/tree/master/require
[assert]: https://github.com/stretchr/testify/tree/master/assert

#### RSpec tests

It is possible to write end-to-end RSpec tests that run against a full
Gitaly server. This is more or less equivalent to the service-level
tests we write in Go. You can also write unit tests for Ruby code in
RSpec.

Because the RSpec tests use a full Gitaly server you must re-compile
Gitaly every time you change the Go code. Run `make` to recompile.

Then, you can run RSpec tests in the `ruby` subdirectory.

```
cd ruby
bundle exec rspec
```

### Rails tests

To use your custom Gitaly when running Rails tests in GDK, go to the
`gitlab` directory in your GDK and follow the instructions at
[Running tests with a locally modified version of Gitaly][custom-gitaly].


[custom-gitaly]: https://docs.gitlab.com/ee/development/gitaly.html#running-tests-with-a-locally-modified-version-of-gitaly
[gdk]: https://gitlab.com/gitlab-org/gitlab-development-kit/#getting-started
[git-remote]: https://git-scm.com/book/en/v2/Git-Basics-Working-with-Remotes
[gitaly]: https://gitlab.com/gitlab-org/gitaly
[gitaly-proto]: https://gitlab.com/gitlab-org/gitaly/tree/master/proto
[gitaly-issue]: https://gitlab.com/gitlab-org/gitaly/issues
[gitlab]: https://gitlab.com
[go-workspace]: https://golang.org/doc/code.html#Workspaces
[proto-docs]: https://developers.google.com/protocol-buffers/docs/overview
