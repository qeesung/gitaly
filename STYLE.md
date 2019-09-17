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

## Stubs

Stubs should be put in the file they're expected to end up in and not in `server.go`.
So for example `BlobService::GetBlob` should end up in `internal/service/blob/get_blob.go`.
This is to guard against merge conflicts, and to make it easier to find.

To minimize diffs (and things to review in MRs) we implement the stubs as if it were 
being used, even though it isn't, and should end up something like this:
```
func (s *server) GetBlob(in *pb.GetBlobRequest, stream pb.BlobService_GetBlobServer) error {
    return helper.Unimplemented
}
```  
instead of:  
```
func (server) GetBlob(_ *pb.GetBlobRequest, _ pb.BlobService_GetBlobServer) error {
    return helper.Unimplemented
}
```

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

