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

## Literals and constructors

### Use "address of struct" instead of new

The following are equivalent in Go:

```golang
# Preferred
foo := &Foo{}

# Don't use
foo := new(Foo)
```

There is no strong reason to prefer one over the other. But mixing
them is unnecessary. We prefer the first style.

### Use hexadecimal byte literals in strings

Sometimes you want to use a byte literal in a Go string. Use a
hexadecimal literal in that case. Unless you have a good reason to use
octal of course.

```golang
# Preferred
foo := "bar\x00baz"

# Don't use octal
foo := "bar\000baz"
```

Octal has the bad property that to represent high bytes, you need 3
digits, but then you may not use a `4` as the first digit. 0377 equals
255 which is a valid byte value. 0400 equals 256 which is not. With
hexadecimal you cannot make this mistake because the largest two digit
hex number is 0xff which equals 255.

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

### Is A Goroutine Necessary?

Avoid using goroutines if the job at hand can be done just as easily and just as well without them.

### Background Task Goroutines

These are goroutines we expect to run the entire life of the process. If they
crash, we expect them to be restarted. If they restart often, we may want a way
to delay subsequent restarts to prevent resource consumption. See
[`dontpanic.GoForever`] for a useful function to handle goroutine restarts with
Sentry observability.

### RPC Goroutines

These are goroutines created to help handle an RPC. A goroutine that is started
during an RPC will also need to end when the RPC completes. This quality makes
it easy to reason about goroutine cleanup.

#### Defer-based Cleanup

One of the safest ways to clean up goroutines (as well as other resources) is
via deferred statements. For example:

```go
func (scs SuperCoolService) MyAwesomeRPC(ctx context.Context, r Request) error {
    done := make(chan struct{}) // signals the goroutine is done
    defer func() { <-done }() // wait until the goroutine is done
    
    go func() {
        defer close(done)    // signal when the goroutine returns
	doWork(r)
    }()
    
    return nil
}
```

Note the heavy usage of defer statements. Using defer statements means that
clean up will occur even if a panic bubbles up the call stack (**IMPORTANT**).
Also, the resource cleanup will
occur in a predictable manner since each defer statement is pushed onto a LIFO
stack of defers. Once the function ends, they are popped off one by one.

### Goroutine Panic Risks

Additionally, every new goroutine has the potential to crash the process. Any
unrecovered panic can cause the entire process to crash and take out any in-
flight requests (**VERY BAD**). When writing code that creates a goroutine,
consider the following question: How confident are you that the code in the
goroutine won't panic? If you can't answer confidently, you may want to use a
helper function to handle panic recovery: [`dontpanic.Go`].

### Limiting Goroutines

When spawning goroutines, you should always be aware of how many goroutines you
will be creating. While cheap, goroutines are not free. Consult the following
questions if you need help deciding if goroutines are being improperly used:

1. How many goroutines will it take the task/RPC to complete?
   - Fixed number - 👍 Good
   - Variable number - 👇 See next question...
1. Does the goroutine count scale with a configuration value (e.g. storage
   locations or concurrency limit)?
   - Yes - 👍 Good
   - No - 🚩 this is a red flag! An RPC where the goroutines do not scale
     predictably will open up the service to denial of service attacks.

[`dontpanic.GoForever`]: https://pkg.go.dev/gitlab.com/gitlab-org/gitaly/internal/dontpanic?tab=doc#GoForever
[`dontpanic.Go`]: https://pkg.go.dev/gitlab.com/gitlab-org/gitaly/internal/dontpanic?tab=doc#Go
