# Gitaly code style

## Errors

### Use %v when wrapping errors

Use `%v` when wrapping errors with context.

    fmt.Errorf("foo context: %v", err)

### Keep errors short

It is customary in Go to pass errors up the call stack and decorate
them. To be a good neighbor to the rest of the call stack we should keep
our errors short.

    // Good
    fmt.Errorf("peek diff line: %v", err)

    // Too long
    fmt.Errorf("ParseDiffOutput: Unexpected error while peeking: %v", err)

### Use lower case in errors

Use lower case in errors; it is OK to preserve upper case in names.

### Errors should stick to the facts

It is tempting to write errors that explain the problem that occurred.
This can be appropriate in some end-user facing situations, but it is
never appropriate for internal error messages. When your
interpretation is wrong it puts the reader on the wrong track.

Stick to the facts. Often it is enough to just describe in a few words
what we were trying to do.

### Use %q when interpolating strings

Unless it would lead to incorrect results, always use `%q` when
interpolating strings. The `%q` operator quotes strings and escapes
spaces and non-printable characters. This can save a lot of debugging
time.

## Return statements

### Don't use "naked return"

In a function with named return variables it is valid to have a plain
("naked") `return` statement, which will return the named return
variables.

In Gitaly we don't use this feature. If the function returns one or
more values, then always pass them to `return`.

## Tests

### Table-driven tests

We like table-driven tests ([Table-driven tests using subtests](https://blog.golang.org/subtests#TOC_4.), [Cheney blog post], [Golang wiki]).

-   Use [subtests](https://blog.golang.org/subtests#TOC_4.) with your table-driven tests, using `t.Run`:

```
func TestTime(t *testing.T) {
    testCases := []struct {
        gmt  string
        loc  string
        want string
    }{
        {"12:31", "Europe/Zuri", "13:31"},
        {"12:31", "America/New_York", "7:31"},
        {"08:08", "Australia/Sydney", "18:08"},
    }
    for _, tc := range testCases {
        t.Run(fmt.Sprintf("%s in %s", tc.gmt, tc.loc), func(t *testing.T) {
            loc, err := time.LoadLocation(tc.loc)
            if err != nil {
                t.Fatal("could not load location")
            }
            gmt, _ := time.Parse("15:04", tc.gmt)
            if got := gmt.In(loc).Format("15:04"); got != tc.want {
                t.Errorf("got %s; want %s", got, tc.want)
            }
        })
    }
}
```

  [Cheney blog post]: https://dave.cheney.net/2013/06/09/writing-table-driven-tests-in-go
  [Golang wiki]: https://github.com/golang/go/wiki/TableDrivenTests

## Black box and white box testing

The dominant style of testing in Gitaly is "white box" testing, meaning
test functions for package `foo` declare their own package also to be
`package foo`. This gives the test code access to package internals. Go
also provides a mechanism sometimes called "black box" testing where the
test functions are not part of the package under test: you write
`package foo_test` instead. Depending on your point of view, the lack of
access to package internals when using black-box is either a bug or a
feature.

As a team we are currently divided on which style to prefer so we are
going to allow both. In areas of the code where there is a clear
pattern, please stick with the pattern. For example, almost all our
service tests are white box.

## Prometheus metrics

Prometheus is a great tool to collect data about how our code behaves in
production. When adding new Prometheus metrics, please follow the [best
practices](https://prometheus.io/docs/practices/naming/) and be aware of
the
[gotchas](https://prometheus.io/docs/practices/instrumentation/#things-to-watch-out-for).

## Git Commands

Gitaly relies heavily on spawning git subprocesses to perform work. Any git
commands spawned from Go code should use the constructs found in
[`safecmd.go`](internal/git/safecmd.go). These constructs, all beginning with
`Safe`, help prevent certain kinds of flag injection exploits. Proper usage is
important to mitigate these injection risks:

- When toggling an option, prefer a longer flag over a short flag for
  readability.
	- Desired: `git.Flag{"--long-flag"}` is easier to read and audit
	- Undesired: `git.Flag{"-L"}`
- When providing a variable to configure a flag, make sure to include the
  variable after an equal sign
	- Desired: `[]git.Flag{"-a="+foo}` prevents flag injection
	- Undesired: `[]git.Flag("-a"+foo)` allows flag injection
- Always define a flag's name via a constant, never use a variable:
	- Desired: `[]git.Flag{"-a"}`
	- Undesired: `[]git.Flag{foo}` is ambiguous and difficult to audit

## Go Imports Style

When adding new package dependencies to a source code file, keep all standard
library packages in one contiguous import block, and all third party packages
(which includes Gitaly packages) in another contiguous block. This way, the
goimports tool will deterministically sort the packages which reduces the noise
in reviews.

Example of **valid** usage:

```go
import (
	"context"
	"io"
	"os/exec"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
)
```

Example of **invalid** usage:

```go
import (
	"io"
	"os/exec"
	
	"context"
	
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	
	"gitlab.com/gitlab-org/gitaly/internal/command"
)
```

## Goroutine Guidelines

Gitaly is a long lived process. This means that every goroutine spawned carries
liability until either the goroutine ends or the program exits. Some goroutines
are expected to run until program termination (e.g. server listeners and file
walkers). However, the vast majority of goroutines spawned are in response to
an RPC, and in most cases should end before the RPC returns. Proper cleanup of
goroutines is crucial to prevent leaks. When in doubt, you can consult the
following guide:

### Background Task Goroutines

These are goroutines we expect to run the entire life of the process. If they
crash, we expect them to be restarted. If they restart often, we may want a way
to delay subsequent restarts to prevent resource consumption. See
[`dontpanic.GoForever`] for a useful function to handle goroutine restarts with
Sentry observability.

### RPC Goroutines

These are goroutines created to help handle an RPC. Except for in rare
conditions, a goroutine that is started during an RPC will also need
to end before the RPC returns. This quality of most goroutines makes it easy to
reason about goroutine cleanup.

#### Defer-based Cleanup

One of the safest ways to clean up goroutines (as well as other resources) is
via deferred statements. For example:

```go
func (scs SuperCoolService) MyAwesomeRPC(ctx context.Context, r Request) error {
    xCh := make(chan Request)
    done := make(chan struct{}) // signals the goroutine is done
    
    defer func() {
        close(xCh) // signals the RPC is done, stop processing work
        <-done     // waits until the goroutine is done
    }()
    
    go func() {
        defer close(done)    // signal when the goroutine returns
        for x := range xCh { // consume values until the channel is closed
            fmt.Println(xCh)
        }
    }()
    
    xCh <- r
    
    return nil
}
```

Note the heavy usage of defer statements. Using defer statements means that
clean up will occur even if a panic bubbles up the call stack (**IMPORTANT**).
Also, the resource cleanup will
occur in a predictable manner since each defer statement is pushed onto a LIFO
stack of defers. Once the function ends, they are popped off one by one.

#### Context-Done Cleanup

Sometimes you may have a difficult or impossible time figuring out how to apply
the above pattern. In those cases, you may want to rely on the `<-ctx.Done`
pattern:

```go
func (scs SuperCoolService) MyAwesomeRPC(ctx context.Context, r Request) error {
    rCh := make(chan Request)
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case r :=<-rCh:
                doSomething(r)
            }
        }
    }()
    
    rCh <- r
    
    return nil
}
```

This pattern works in RPC's because the context will always be cancelled after
the RPC completes.

#### Confirming Goroutine Cleanup

No matter which way you decide to clean up a goroutine, you always need to be
confident that it does in fact get cleaned up. The simplest way to do this in
an RPC is to simply wait until the clean up happens. If this is not reasonable,
you may want to rely on designing the goroutine as simple as possible so that
it can be easily verified by code review.

Why is this so important?

- We lose observability - most of our metrics are derived from the performance
  of the RPC. If we perform the real work outside the RPC, and the RPC returns
  immediately, we end up with a false sense of how slow/fast an RPC is.
- Goroutine leaks - if the goroutines never cleanup, we can end up with a leak.
  While goroutines are much cheaper than threads or processes, they still incur
  a cost. A goroutine starts with a minimum of 2KB stack space and grows.

### Goroutine Panic Risks

Additionally, every new goroutine has the potential to crash the process. Any
unrecovered panic can cause the entire process to crash and take out any in-
flight requests (**VERY BAD**). When writing code that creates a goroutine,
consider the following question: How confident are you that the code in the
goroutine won't panic? If you can't answer confidently, you may want to use a
helper function to handle panic recovery: [`dontpanic.Go`].

### Limiting Goroutines

When spawning goroutines, you should always be aware of how many goroutines you
will be creating. While cheap, goroutines are not free. If it is not known how
many goroutines are needed to complete a request, that is a red flag. It is
possible that the RPC can end up generating many goroutines for every client
request and enable a denial of service attack.

[`dontpanic.GoForever`]: https://pkg.go.dev/gitlab.com/gitlab-org/gitaly/internal/dontpanic?tab=doc#GoForever
[`dontpanic.Go`]: https://pkg.go.dev/gitlab.com/gitlab-org/gitaly/internal/dontpanic?tab=doc#Go
