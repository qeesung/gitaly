package sidechannel

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/listenmux"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var magicBytes = []byte("sidechannel")

// sidechannelTimeout is the timeout for establishing a sidechannel
// connection. The sidechannel is supposed to be opened on the same wire with
// incoming grpc request. There won't be real handshaking involved, so it
// should be fast.
const (
	sidechannelTimeout     = 5 * time.Second
	sidechannelMetadataKey = "gitaly-sidechannel-id"
)

// RetryableError is an error for which we can instruct the client to retry the
// whole RPC once more.
type RetryableError struct{ error }

// OpenSidechannel opens a sidechannel connection from the stream opener
// extracted from the current peer connection.
func OpenSidechannel(ctx context.Context) (_ *ServerConn, err error) {
	span, ctx := tracing.StartSpanIfHasParent(ctx, "sidechannel.OpenSidechannel", nil)
	defer span.Finish()

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("sidechannel: failed to extract incoming metadata")
	}
	ids := md.Get(sidechannelMetadataKey)
	if len(ids) == 0 {
		return nil, fmt.Errorf("sidechannel: sidechannel-id not found in incoming metadata")
	}
	sidechannelID, _ := strconv.ParseInt(ids[len(ids)-1], 10, 64)

	muxSession, err := backchannel.GetYamuxSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("sidechannel: fail to extract yamux session: %w", err)
	}

	stream, err := muxSession.Open()
	if err != nil {
		return nil, RetryableError{error: fmt.Errorf("sidechannel: open stream: %w", err)}
	}
	defer func() {
		if err != nil {
			stream.Close()
		}
	}()

	if err := stream.SetDeadline(time.Now().Add(sidechannelTimeout)); err != nil {
		return nil, err
	}

	if _, err := stream.Write(magicBytes); err != nil {
		return nil, RetryableError{error: fmt.Errorf("sidechannel: write magic bytes: %w", err)}
	}

	if err := binary.Write(stream, binary.BigEndian, sidechannelID); err != nil {
		return nil, RetryableError{error: fmt.Errorf("sidechannel: write stream id: %w", err)}
	}

	buf := make([]byte, 2)
	if _, err := io.ReadFull(stream, buf); err != nil {
		return nil, RetryableError{error: fmt.Errorf("sidechannel: receive confirmation: %w", err)}
	}
	if string(buf) != "ok" {
		return nil, fmt.Errorf("sidechannel: expected ok, got %q", buf)
	}

	if err := stream.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}

	return newServerConn(stream), nil
}

// RegisterSidechannel registers the caller into the waiting list of the
// sidechannel registry and injects the sidechannel ID into outgoing metadata.
// The caller is expected to establish the request with the returned context. The
// callback is executed automatically when the sidechannel connection arrives.
// The result is pushed to the error channel of the returned waiter.
func RegisterSidechannel(ctx context.Context, registry *Registry, callback func(*ClientConn) error) (context.Context, *Waiter) {
	waiter := registry.Register(callback)
	ctxOut := metadata.AppendToOutgoingContext(ctx, sidechannelMetadataKey, fmt.Sprintf("%d", waiter.id))
	return ctxOut, waiter
}

// ServerHandshaker implements the server-side sidechannel handshake.
type ServerHandshaker struct {
	registry *Registry
}

// Magic returns the magic bytes for sidechannel
func (s *ServerHandshaker) Magic() string {
	return string(magicBytes)
}

// Handshake implements the handshaking logic for sidechannel so that
// this handshaker reads the sidechannel ID from the wire, and then delegates
// the connection to the sidechannel registry
func (s *ServerHandshaker) Handshake(conn net.Conn, authInfo credentials.AuthInfo) (net.Conn, credentials.AuthInfo, error) {
	var sidechannelID sidechannelID
	if err := binary.Read(conn, binary.BigEndian, &sidechannelID); err != nil {
		return nil, nil, fmt.Errorf("sidechannel: fail to extract sidechannel ID: %w", err)
	}

	if err := s.registry.receive(sidechannelID, conn); err != nil {
		return nil, nil, err
	}

	// credentials.ErrConnDispatched, indicating that the connection is already
	// dispatched out of gRPC. gRPC should leave it alone and exit in peace.
	return nil, nil, credentials.ErrConnDispatched
}

// NewServerHandshaker creates a new handshaker for sidechannel to
// embed into listenmux.
func NewServerHandshaker(registry *Registry) *ServerHandshaker {
	return &ServerHandshaker{registry: registry}
}

// NewClientHandshaker is used to enable sidechannel support on outbound
// gRPC connections.
func NewClientHandshaker(logger log.Logger, registry *Registry) client.Handshaker {
	cfg := backchannel.DefaultConfiguration()
	// If a client hangs up while the server is writing data to it then the
	// server will block for 5 minutes by default before erroring out. This
	// makes testing difficult and there is no reason to have such a long
	// timeout in the case of sidechannels. A 1 second timeout is also OK.
	cfg.StreamCloseTimeout = time.Second

	return backchannel.NewClientHandshaker(
		logger,
		func() backchannel.Server {
			lm := listenmux.New(insecure.NewCredentials())
			lm.Register(NewServerHandshaker(registry))
			return grpc.NewServer(grpc.Creds(lm))
		},
		cfg,
	)
}
