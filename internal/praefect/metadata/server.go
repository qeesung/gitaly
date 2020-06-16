package metadata

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const (
	// PraefectMetadataKey is the key used to store Praefect server
	// information in the gRPC metadata.
	PraefectMetadataKey = "praefect-server"
	// PraefectEnvKey is the key used to store Praefect server information
	// in environment variables.
	PraefectEnvKey = "PRAEFECT_SERVER"
)

var (
	// ErrPraefectServerNotFound indicates the Praefect server metadata
	// could not be found
	ErrPraefectServerNotFound = errors.New("metadata for Praefect server not found")
)

// PraefectServer stores parameters required to connect to a Praefect server
type PraefectServer struct {
	// ListenAddr is the TCP listen address of the Praefect server
	ListenAddr string `json:"listen_addr"`
	// SocketPath is the Unix socket path of the Praefect server
	SocketPath string `json:"socket_path"`
	// Token is the token required to authenticate with the Praefect server
	Token string `json:"token"`
}

// InjectPraefectServer injects Praefect connection metadata into an incoming context
func InjectPraefectServer(ctx context.Context, conf config.Config) (context.Context, error) {
	praefectServer := PraefectServer{
		ListenAddr: strings.TrimPrefix(conf.ListenAddr, "tcp://"),
		SocketPath: strings.TrimPrefix(conf.SocketPath, "unix://"),
		Token:      conf.Auth.Token,
	}

	marshalled, err := json.Marshal(praefectServer)
	if err != nil {
		return nil, err
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	} else {
		md = md.Copy()
	}
	md.Set(PraefectMetadataKey, base64.StdEncoding.EncodeToString(marshalled))

	return metadata.NewIncomingContext(ctx, md), nil
}

// Resolve Praefect address based on its peer information. Depending on how
// Praefect reached out to us, we'll adjust the PraefectServer to contain
// either its Unix or TCP address.
func (p *PraefectServer) resolvePraefectAddress(peer *peer.Peer) error {
	switch addr := peer.Addr.(type) {
	case *net.UnixAddr:
		if p.SocketPath == "" {
			return errors.New("resolvePraefectAddress: got Unix peer but no socket path")
		}

		p.ListenAddr = ""
		return nil
	case *net.TCPAddr:
		if p.ListenAddr == "" {
			return errors.New("resolvePraefectAddress: got TCP peer but no listen address")
		}

		// We need to replace Praefect's IP address with the peer's
		// address as the value we have is from Praefect's configuration,
		// which may be a wildcard IP address ("0.0.0.0").
		_, port, err := net.SplitHostPort(p.ListenAddr)
		if err != nil {
			return fmt.Errorf("resolvePraefectAddress: could not resolve address %q: %w", p.ListenAddr, err)
		}

		p.ListenAddr = net.JoinHostPort(addr.IP.String(), port)
		p.SocketPath = ""

		return nil
	default:
		return fmt.Errorf("resolvePraefectAddress: unknown peer address scheme: %s", peer.Addr.Network())
	}
}

// PraefectFromContext extracts `PraefectServer` from an incoming context. In
// case the metadata key is not set, the function will return `ErrPraefectServerNotFound`.
func PraefectFromContext(ctx context.Context) (*PraefectServer, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, ErrPraefectServerNotFound
	}

	encoded := md[PraefectMetadataKey]
	if len(encoded) == 0 {
		return nil, ErrPraefectServerNotFound
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded[0])
	if err != nil {
		return nil, fmt.Errorf("PraefectFromContext: %w", err)
	}

	var praefect PraefectServer
	if err := json.Unmarshal(decoded, &praefect); err != nil {
		return nil, fmt.Errorf("PraefectFromContext: %w", err)
	}

	peer, ok := peer.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("PraefectFromContext: could not get peer")
	}

	if err := praefect.resolvePraefectAddress(peer); err != nil {
		return nil, err
	}

	return &praefect, nil
}

// PraefectFromEnv extracts `PraefectServer` from the environment variable
// `PraefectEnvKey`. In case the variable is not set, the function will return
// `ErrPraefectServerNotFound`.
func PraefectFromEnv(envvars []string) (*PraefectServer, error) {
	praefectKey := fmt.Sprintf("%s=", PraefectEnvKey)
	praefectEnv := ""
	for _, envvar := range envvars {
		if strings.HasPrefix(envvar, praefectKey) {
			praefectEnv = envvar[len(praefectKey):]
			break
		}
	}
	if praefectEnv == "" {
		return nil, ErrPraefectServerNotFound
	}

	decoded, err := base64.StdEncoding.DecodeString(praefectEnv)
	if err != nil {
		return nil, fmt.Errorf("failed decoding base64: %w", err)
	}

	p := PraefectServer{}
	if err := json.Unmarshal(decoded, &p); err != nil {
		return nil, err
	}

	return &p, nil
}

// Env encodes the `PraefectServer` and returns an environment variable.
func (p *PraefectServer) Env() (string, error) {
	marshalled, err := json.Marshal(p)
	if err != nil {
		return "", err
	}

	encoded := base64.StdEncoding.EncodeToString(marshalled)
	return fmt.Sprintf("%s=%s", PraefectEnvKey, encoded), nil
}

func (p *PraefectServer) Address() (string, error) {
	if p.SocketPath != "" {
		return "unix://" + p.SocketPath, nil
	}

	if p.ListenAddr != "" {
		return "tcp://" + p.ListenAddr, nil
	}

	return "", errors.New("no address configured")
}

// Dial will try to connect to the given Praefect server
func (p *PraefectServer) Dial(ctx context.Context) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithBlock(),
	}
	if p.Token != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(p.Token)))
	}

	address, err := p.Address()
	if err != nil {
		return nil, err
	}

	return client.DialContext(ctx, address, opts)
}
