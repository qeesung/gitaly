package testhelper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"golang.org/x/exp/slices"
)

const (
	// RepositoryAuthToken is the default token used to authenticate
	// against other Gitaly servers. It is inject as part of the
	// GitalyServers metadata.
	RepositoryAuthToken = "the-secret-token"
	// DefaultStorageName is the default name of the Gitaly storage.
	DefaultStorageName = "default"
)

// IsReftableEnabled returns whether the git reftable is enabled
func IsReftableEnabled() bool {
	_, ok := os.LookupEnv("GIT_DEFAULT_REF_FORMAT")
	return ok
}

// SkipWithReftable skips the test when reftable is being used.
func SkipWithReftable(tb testing.TB, reason string) {
	if IsReftableEnabled() {
		tb.Skip(reason)
	}
}

// IsWALEnabled returns whether write-ahead logging is enabled in this testing run.
func IsWALEnabled() bool {
	_, ok := os.LookupEnv("GITALY_TEST_WAL")
	return ok
}

// SkipWithWAL skips the test if write-ahead logging is enabled in this testing run. A reason
// should be provided either as a description or a link to an issue to explain why the test is
// skipped.
func SkipWithWAL(tb testing.TB, reason string) {
	if IsWALEnabled() {
		tb.Skip(reason)
	}
}

// WithOrWithoutWAL returns a value correspondingly to if WAL is enabled or not.
func WithOrWithoutWAL[T any](walVal, noWalVal T) T {
	if IsWALEnabled() {
		return walVal
	}
	return noWalVal
}

// IsPraefectEnabled returns whether this testing run is done with Praefect in front of the Gitaly.
func IsPraefectEnabled() bool {
	_, enabled := os.LookupEnv("GITALY_TEST_WITH_PRAEFECT")
	return enabled
}

// SkipWithPraefect skips the test if it is being executed with Praefect in front
// of the Gitaly.
func SkipWithPraefect(tb testing.TB, reason string) {
	if IsPraefectEnabled() {
		tb.Skipf(reason)
	}
}

// MustReadFile returns the content of a file or fails at once.
func MustReadFile(tb testing.TB, filename string) []byte {
	tb.Helper()

	content, err := os.ReadFile(filename)
	if err != nil {
		tb.Fatal(err)
	}

	return content
}

// WriteFiles writes a map of files to the filesystem where the map key is the
// filename relative to root and the value is one of string, []byte or
// io.Reader.
func WriteFiles(tb testing.TB, root string, files map[string]any) {
	tb.Helper()

	require.DirExists(tb, root)

	for name, value := range files {
		path := filepath.Join(root, name)

		require.NoError(tb, os.MkdirAll(filepath.Dir(path), perm.SharedDir))

		switch content := value.(type) {
		case string:
			require.NoError(tb, os.WriteFile(path, []byte(content), perm.PublicFile))
		case []byte:
			require.NoError(tb, os.WriteFile(path, content, perm.PublicFile))
		case io.Reader:
			func() {
				f, err := os.Create(path)
				require.NoError(tb, err)
				defer MustClose(tb, f)

				_, err = io.Copy(f, content)
				require.NoError(tb, err)
			}()
		default:
			tb.Fatalf("WriteFiles: %q: unsupported file content type %T", path, value)
		}
	}
}

// MustRunCommand runs a command with an optional standard input and returns the standard output, or fails.
func MustRunCommand(tb testing.TB, stdin io.Reader, name string, args ...string) []byte {
	tb.Helper()

	if filepath.Base(name) == "git" {
		require.Fail(tb, "Please use gittest.Exec or gittest.ExecStream to run git commands.")
	}

	cmd := exec.Command(name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}

	output, err := cmd.Output()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		require.NoError(tb, err, "%s %s: %s", name, args, exitErr.Stderr)
	}

	return output
}

// MustClose calls Close() on the Closer and fails the test in case it returns
// an error. This function is useful when closing via `defer`, as a simple
// `defer require.NoError(t, closer.Close())` would cause `closer.Close()` to
// be executed early already.
func MustClose(tb testing.TB, closer io.Closer) {
	require.NoError(tb, closer.Close())
}

// Server is an interface for a server that can serve requests on a specific listener. This
// interface is used by the MustServe helper function.
type Server interface {
	Serve(net.Listener) error
}

// MustServe starts to serve the given server with the listener. This function asserts that the
// server was able to successfully serve and is useful in contexts where one wants to simply spawn a
// server in a Goroutine.
func MustServe(tb testing.TB, server Server, listener net.Listener) {
	tb.Helper()

	// `http.Server.Serve()` is expected to return `http.ErrServerClosed`, so we special-case
	// this error here.
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		require.NoError(tb, err)
	}
}

// CopyFile copies a file at the path src to a file at the path dst
func CopyFile(tb testing.TB, src, dst string) {
	fsrc, err := os.Open(src)
	require.NoError(tb, err)
	defer MustClose(tb, fsrc)

	fdst, err := os.Create(dst)
	require.NoError(tb, err)
	defer MustClose(tb, fdst)

	_, err = io.Copy(fdst, fsrc)
	require.NoError(tb, err)
}

// GetTemporaryGitalySocketFileName will return a unique, useable socket file name
func GetTemporaryGitalySocketFileName(tb testing.TB) string {
	require.NotEmpty(tb, testDirectory, "you must call testhelper.Configure() before GetTemporaryGitalySocketFileName()")

	tmpfile, err := os.CreateTemp(testDirectory, "gitaly.socket.")
	require.NoError(tb, err)

	name := tmpfile.Name()
	require.NoError(tb, tmpfile.Close())
	require.NoError(tb, os.Remove(name))

	return name
}

// GetLocalhostListener listens on the next available TCP port and returns
// the listener and the localhost address (host:port) string.
func GetLocalhostListener(tb testing.TB) (net.Listener, string) {
	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(tb, err)

	addr := fmt.Sprintf("localhost:%d", l.Addr().(*net.TCPAddr).Port)

	return l, addr
}

// ContextOpt returns a new context instance with the new additions to it.
type ContextOpt func(context.Context) context.Context

// Context returns that gets canceled at the end of the test.
func Context(tb testing.TB, opts ...ContextOpt) context.Context {
	ctx, cancel := context.WithCancel(ContextWithoutCancel(opts...))
	tb.Cleanup(cancel)
	return ctx
}

// ContextWithSimulatedTimeout creates a new context and allows the caller to simulate a timeout event. It returns a new
// context, a cancelation function, and a function that triggers the timeout event. After timeout, ctx.Done() channel
// is closed and ctx.Err() returns context.DeadlineExceeded. The context can be cancelled earlier by calling the
// returned cancelation function. This behavior is similar to any context returned from context.WithTimeout() or
// context.WithDeadline().
func ContextWithSimulatedTimeout(ctx context.Context) (context.Context, context.CancelFunc, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	tctx := &timeoutContext{ctx}
	return tctx, func() { cancel(nil) }, func() { cancel(errSimulatedDeadlineExceeded) }
}

var errSimulatedDeadlineExceeded = fmt.Errorf("simulated deadline exceeded")

type timeoutContext struct {
	context.Context
}

func (c *timeoutContext) Err() error {
	if c.Context.Err() == nil {
		return nil
	}
	if err := context.Cause(c); errors.Is(err, errSimulatedDeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return c.Context.Err()
}

// ContextWithoutCancel returns a non-cancellable context.
func ContextWithoutCancel(opts ...ContextOpt) context.Context {
	ctx := context.Background()

	// !!! Please don't delete the lines creating `rnd` below !!!
	// We shouldn't use code like `rand.Int()%2` without the code
	// below anymore. We did use that in the past but it created
	// issues. Now please use `rnd.Int()%2` without removing the
	// code below. See:
	// https://gitlab.com/gitlab-org/gitaly/-/merge_requests/4568/diffs?commit_id=1bfd54e59d540100c90387f374e43458283290a3
	t := time.Now()
	rnd := rand.New(rand.NewSource(t.Unix() + int64(t.Nanosecond())))

	// Enable use of explicit feature flags. Each feature flag which is checked must have been
	// explicitly injected into the context, or otherwise we panic. This is a sanity check to
	// verify that all feature flags we introduce are tested both with the flag enabled and
	// with the flag disabled.
	ctx = featureflag.ContextWithExplicitFeatureFlags(ctx)
	// There are some feature flags we need to enable in this function because they end up very
	// deep in the call stack, so almost every test function would have to inject it into its
	// context. The values of these flags should be randomized to increase the test coverage.

	// Randomly enable mailmap
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.MailmapOptions, rnd.Int()%2 == 0)
	// Randomly enable limiter.resizableSemaphore
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.UseResizableSemaphoreInConcurrencyLimiter, rnd.Int()%2 == 0)
	// Disable LogGitTraces
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.LogGitTraces, false)
	// Globably disable autocrlf config in git.
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.AutocrlfConfig, true)

	// Randomly enable either Git v2.43 or Git v2.44
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.GitV244, rnd.Int()%2 == 0)

	for _, opt := range opts {
		ctx = opt(ctx)
	}

	return ctx
}

// CreateGlobalDirectory creates a directory in the test directory that is shared across all
// between all tests.
func CreateGlobalDirectory(tb testing.TB, name string) string {
	require.NotEmpty(tb, testDirectory, "global temporary directory does not exist")
	path := filepath.Join(testDirectory, name)
	require.NoError(tb, os.Mkdir(path, perm.PublicDir))
	return path
}

// TempDir is a wrapper around os.MkdirTemp that provides a cleanup function.
func TempDir(tb testing.TB) string {
	if testDirectory == "" {
		panic("you must call testhelper.Configure() before TempDir()")
	}

	tmpDir, err := os.MkdirTemp(testDirectory, "")
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, os.RemoveAll(tmpDir))
	})

	return tmpDir
}

// Cleanup functions should be called in a defer statement
// immediately after they are returned from a test helper
type Cleanup func()

// WriteExecutable ensures that the parent directory exists, and writes an executable with provided
// content. The executable must not exist previous to writing it. Returns the path of the written
// executable.
func WriteExecutable(tb testing.TB, path string, content []byte) string {
	dir := filepath.Dir(path)
	require.NoError(tb, os.MkdirAll(dir, perm.SharedDir))
	tb.Cleanup(func() {
		assert.NoError(tb, os.RemoveAll(dir))
	})

	// Open the file descriptor and write the script into it. It may happen that any other
	// Goroutine forks while we hold this writeable file descriptor, and as a consequence we
	// leak it into the other process. Subsequently, even if we close the file descriptor
	// ourselves this other process may still hold on to the writeable file descriptor. The
	// result is that calls to execve(3P) on our just-written file will fail with ETXTBSY,
	// which is raised when trying to execute a file which is still open to be written to.
	//
	// We thus need to perform file locking to ensure that all writeable references to this
	// file have been closed before returning.
	executable, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm.SharedExecutable)
	require.NoError(tb, err)
	_, err = io.Copy(executable, bytes.NewReader(content))
	require.NoError(tb, err)

	// We now lock the file descriptor for exclusive access. If there was a forked process
	// holding the writeable file descriptor at this point in time, then it would refer to the
	// same file descriptor and thus be locked for exclusive access, as well. If we fork after
	// creating the lock and before closing the writeable file descriptor, then the dup'd file
	// descriptor would automatically inherit the lock.
	//
	// No matter what, after this step any file descriptors referring to this writeable file
	// descriptor will be exclusively locked.
	require.NoError(tb, syscall.Flock(int(executable.Fd()), syscall.LOCK_EX))

	// We now close this file. The file will be automatically unlocked as soon as all
	// references to this file descriptor are closed.
	MustClose(tb, executable)

	// We now open the file again, but this time only for reading.
	executable, err = os.Open(path)
	require.NoError(tb, err)

	// And this time, we try to acquire a shared lock on this file. This call will block until
	// the exclusive file lock on the above writeable file descriptor has been dropped. So as
	// soon as we're able to acquire the lock we know that there cannot be any open writeable
	// file descriptors for this file anymore, and thus we won't get ETXTBSY anymore.
	require.NoError(tb, syscall.Flock(int(executable.Fd()), syscall.LOCK_SH))
	MustClose(tb, executable)

	return path
}

var umask perm.Umask = func() perm.Umask {
	oldMask := syscall.Umask(0)
	syscall.Umask(oldMask)
	return perm.Umask(oldMask)
}()

// Umask return the umask of the current procses. Note that this value is computed once at initialization time because
// it is shared global state that is unsafe to access when there are multiple threads running at the same time. It
// follows that tests should never update the umask.
func Umask() perm.Umask {
	return umask
}

// Unsetenv unsets an environment variable. The variable will be restored after the test has
// finished.
func Unsetenv(tb testing.TB, key string) {
	tb.Helper()

	// We're first using `tb.Setenv()` here due to two reasons: first, it will automitcally
	// handle restoring the environment variable for us after the test has finished. And second,
	// it performs a check whether we're running with `tb.Parallel()`.
	tb.Setenv(key, "")

	// And now we can unset the environment variable given that we know we're not running in a
	// parallel test and where the cleanup function has been installed.
	require.NoError(tb, os.Unsetenv(key))
}

// GitalyOrPraefect returns either the Gitaly- or Praefect-specific object depending on whether
// tests are running with Praefect as a proxy or not.
func GitalyOrPraefect[Type any](gitaly, praefect Type) Type {
	if IsPraefectEnabled() {
		return praefect
	}
	return gitaly
}

// SkipQuarantinedTest skips the test if the test name has been specified as
// quarantined. If no test names are provided the test is always skipped.
func SkipQuarantinedTest(t *testing.T, issue string, tests ...string) {
	if issue == "" {
		panic("issue not specified")
	}

	if len(tests) == 0 || slices.Contains(tests, t.Name()) {
		t.Skipf("This test has been quarantined. Please see %s for more information.", issue)
	}
}

// pkgPath is used to determine the package path using reflection.
type pkgPath struct{}

// PkgPath returns the gitaly module package path, including major version
// number. paths will be path joined to the returned package path.
func PkgPath(paths ...string) string {
	internalPkgPath := path.Dir(reflect.TypeOf(pkgPath{}).PkgPath())
	rootPkgPath := path.Dir(internalPkgPath)
	return path.Join(append([]string{rootPkgPath}, paths...)...)
}
