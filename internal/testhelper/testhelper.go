package testhelper

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
)

const (
	// RepositoryAuthToken is the default token used to authenticate
	// against other Gitaly servers. It is inject as part of the
	// GitalyServers metadata.
	RepositoryAuthToken = "the-secret-token"
	// DefaultStorageName is the default name of the Gitaly storage.
	DefaultStorageName = "default"
	// SigningKeyPath is the default path to test commit signing.
	SigningKeyPath = "testdata/signingKey.gpg"
)

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

// GitlabTestStoragePath returns the storage path to the gitlab-test repo.
func GitlabTestStoragePath() string {
	if testDirectory == "" {
		panic("you must call testhelper.Configure() before GitlabTestStoragePath()")
	}
	return filepath.Join(testDirectory, "storage")
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
	if err != nil {
		stderr := err.(*exec.ExitError).Stderr
		require.NoError(tb, err, "%s %s: %s", name, args, stderr)
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

// ContextWithLogger allows to inject provided logger into the context.
func ContextWithLogger(logger *log.Entry) ContextOpt {
	return func(ctx context.Context) context.Context {
		return ctxlogrus.ToContext(ctx, logger)
	}
}

// Context returns that gets canceled at the end of the test.
func Context(tb testing.TB, opts ...ContextOpt) context.Context {
	ctx, cancel := context.WithCancel(ContextWithoutCancel(opts...))
	tb.Cleanup(cancel)
	return ctx
}

// ContextWithoutCancel returns a non-cancellable context.
func ContextWithoutCancel(opts ...ContextOpt) context.Context {
	ctx := context.Background()

	t := time.Now()

	rnd := mrand.New(mrand.NewSource(t.Unix() + int64(t.Nanosecond())))

	// Enable use of explicit feature flags. Each feature flag which is checked must have been
	// explicitly injected into the context, or otherwise we panic. This is a sanity check to
	// verify that all feature flags we introduce are tested both with the flag enabled and
	// with the flag disabled.
	ctx = featureflag.ContextWithExplicitFeatureFlags(ctx)
	// There are some feature flags we need to enable in this function because they end up very
	// deep in the call stack, so almost every test function would have to inject it into its
	// context.
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.RunCommandsInCGroup, true)
	// Randomly inject the Git flag so that we have coverage of tests with both old and new Git
	// version by pure chance.
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.GitV2371Gl1, rnd.Int()%2 == 0)
	// PraefectGeneratedReplicaPaths affects many tests as it changes the repository creation logic.
	// Randomly enable the flag to exercise both paths to some extent.
	ctx = featureflag.ContextWithFeatureFlag(ctx, featureflag.PraefectGeneratedReplicaPaths, rnd.Int()%2 == 0)

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
	require.NoError(tb, os.Mkdir(path, 0o777))
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
	require.NoError(tb, os.MkdirAll(dir, 0o755))
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
	executable, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
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

// GenerateCerts creates a certificate that can be used to establish TLS protected TCP connection.
// It returns paths to the file with the certificate and its private key.
func GenerateCerts(tb testing.TB) (string, string) {
	tb.Helper()

	rootCA := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, 1),
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("0.0.0.0"), net.ParseIP("127.0.0.1"), net.ParseIP("::1"), net.ParseIP("::")},
		DNSNames:              []string{"localhost"},
		KeyUsage:              x509.KeyUsageCertSign,
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)

	caCert, err := x509.CreateCertificate(rand.Reader, rootCA, rootCA, &caKey.PublicKey, caKey)
	require.NoError(tb, err)

	entityKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)

	entityX509 := &x509.Certificate{
		SerialNumber: big.NewInt(2),
	}

	entityCert, err := x509.CreateCertificate(rand.Reader, rootCA, entityX509, &entityKey.PublicKey, caKey)
	require.NoError(tb, err)

	certFile, err := os.CreateTemp(testDirectory, "")
	require.NoError(tb, err)
	defer MustClose(tb, certFile)
	tb.Cleanup(func() {
		require.NoError(tb, os.Remove(certFile.Name()))
	})

	// create chained PEM file with CA and entity cert
	for _, cert := range [][]byte{entityCert, caCert} {
		require.NoError(tb,
			pem.Encode(certFile, &pem.Block{
				Type:  "CERTIFICATE",
				Bytes: cert,
			}),
		)
	}

	keyFile, err := os.CreateTemp(testDirectory, "")
	require.NoError(tb, err)
	defer MustClose(tb, keyFile)
	tb.Cleanup(func() {
		require.NoError(tb, os.Remove(keyFile.Name()))
	})

	entityKeyBytes, err := x509.MarshalECPrivateKey(entityKey)
	require.NoError(tb, err)

	require.NoError(tb,
		pem.Encode(keyFile, &pem.Block{
			Type:  "ECDSA PRIVATE KEY",
			Bytes: entityKeyBytes,
		}),
	)

	return certFile.Name(), keyFile.Name()
}
