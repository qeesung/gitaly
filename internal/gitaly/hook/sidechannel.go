package hook

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	gitaly_metadata "gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"google.golang.org/grpc/metadata"
)

const (
	sidechannelHeader = "gitaly-sidechannel-socket"
	sidechannelSocket = "sidechannel"
)

type errInvalidSidechannelAddress struct{ string }

func (e *errInvalidSidechannelAddress) Error() string {
	return fmt.Sprintf("invalid side channel address: %q", e.string)
}

// GetSidechannel looks for a sidechannel address in an incoming context
// and establishes a connection if it finds an address.
func GetSidechannel(ctx context.Context) (net.Conn, error) {
	address := gitaly_metadata.GetValue(ctx, sidechannelHeader)
	if path.Base(address) != sidechannelSocket {
		return nil, &errInvalidSidechannelAddress{address}
	}

	return net.DialTimeout("unix", address, time.Second)
}

// SetupSidechannel creates a sidechannel listener in a tempdir and
// launches a goroutine that will run the callback if the listener
// receives a connection. The address of the listener is stored in the
// returned context, so that the caller can propagate it to a server. The
// caller must Close the SidechannelWaiter to prevent resource leaks.
func SetupSidechannel(ctx context.Context, payload git.HooksPayload, callback func(*net.UnixConn) error) (_ context.Context, _ *SidechannelWaiter, err error) {
	var sidechannelDir, sidechannelName string

	// If there is a runtime directory we try to create a sidechannel directory in there that
	// will hold all the temporary sidechannel subdirectories. Otherwise, we fall back to create
	// the sidechannel directory in the system's temporary directory.
	if payload.RuntimeDir != "" {
		sidechannelDir := filepath.Join(payload.RuntimeDir, "chan.d")

		// Note that we don't use `os.MkdirAll()` here: we don't want to accidentally create
		// the full directory hierarchy, and the assumption is that the runtime directory
		// must exist already.
		if err := os.Mkdir(sidechannelDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, nil, err
		}

		sidechannelName = "*"
	} else {
		sidechannelName = "gitaly*"
	}

	socketDir, err := os.MkdirTemp(sidechannelDir, sidechannelName)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(socketDir)
		}
	}()

	address := path.Join(socketDir, sidechannelSocket)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: address})
	if err != nil {
		return nil, nil, err
	}

	wt := &SidechannelWaiter{
		errC:      make(chan error),
		socketDir: socketDir,
		listener:  l,
	}
	go wt.run(callback)

	ctx = metadata.AppendToOutgoingContext(ctx, sidechannelHeader, address)
	return ctx, wt, nil
}

// SidechannelWaiter provides cleanup and error propagation for a
// sidechannel callback.
type SidechannelWaiter struct {
	errC      chan error
	socketDir string
	listener  *net.UnixListener
}

func (wt *SidechannelWaiter) run(callback func(*net.UnixConn) error) {
	defer close(wt.errC)

	wt.errC <- func() error {
		c, err := wt.listener.AcceptUnix()
		if err != nil {
			return err
		}
		defer func() {
			// Error is already checked below.
			_ = c.Close()
		}()

		// Eagerly remove the socket directory, in case the process exits before
		// wt.Close() can run.
		if err := os.RemoveAll(wt.socketDir); err != nil {
			return err
		}

		if err := callback(c); err != nil {
			return err
		}

		if err := c.Close(); err != nil {
			return err
		}

		return nil
	}()
}

// Close cleans up sidechannel resources. If the callback is already
// running, Close will block until the callback is done.
func (wt *SidechannelWaiter) Close() error {
	// Run all cleanup actions _before_ checking errors, so that we cannot
	// forget one.
	cleanupErrors := []error{
		// If wt.run() is blocked on AcceptUnix(), this will unblock it.
		wt.listener.Close(),
		// Remove the socket directory to prevent garbage in case wt.run() did
		// not run.
		os.RemoveAll(wt.socketDir),
		// Block until wt.run() is done.
		wt.Wait(),
	}

	for _, err := range cleanupErrors {
		if err != nil {
			return err
		}
	}

	return nil
}

// Wait waits for the callback to run and returns its error value.
func (wt *SidechannelWaiter) Wait() error { return <-wt.errC }
