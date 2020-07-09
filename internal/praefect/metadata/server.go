package metadata

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/bootstrap/starter"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const (
	// PraefectMetadataKey is the key used to store Praefect server
	// information in the gRPC metadata.
	PraefectMetadataKey = "gitaly-praefect-server"
	// PraefectEnvKey is the key used to store Praefect server information
	// in environment variables.
	PraefectEnvKey = "GITALY_PRAEFECT_SERVER"
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
	// TLSListenAddr is the TCP listen address of the Praefect server with TLS support
	TLSListenAddr string `json:"tls_listen_addr"`
	// SocketPath is the Unix socket path of the Praefect server
	SocketPath string `json:"socket_path"`
	// Token is the token required to authenticate with the Praefect server
	Token string `json:"token"`
}

// InjectPraefectServer injects Praefect connection metadata into an incoming context
func InjectPraefectServer(ctx context.Context, conf config.Config) (context.Context, error) {
	praefectServer := PraefectServer{Token: conf.Auth.Token}

	addrBySchema := map[string]*string{
		starter.TCP:  &praefectServer.ListenAddr,
		starter.TLS:  &praefectServer.TLSListenAddr,
		starter.Unix: &praefectServer.SocketPath,
	}

	for _, endpoint := range []struct {
		schema string
		addr   string
	}{
		{schema: starter.TCP, addr: conf.ListenAddr},
		{schema: starter.TLS, addr: conf.TLSListenAddr},
		{schema: starter.Unix, addr: conf.SocketPath},
	} {
		if endpoint.addr == "" {
			continue
		}

		parsed, err := starter.ParseEndpoint(endpoint.addr)
		if err != nil {
			if !errors.Is(err, starter.ErrEmptySchema) {
				return nil, err
			}
			parsed = starter.Config{Name: endpoint.schema, Addr: endpoint.addr}
		}

		addr, err := parsed.Endpoint()
		if err != nil {
			return nil, fmt.Errorf("processing of %s: %w", endpoint.schema, err)
		}

		*addrBySchema[endpoint.schema] = addr
	}

	serialized, err := praefectServer.serialize()
	if err != nil {
		return nil, err
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	} else {
		md = md.Copy()
	}
	md.Set(PraefectMetadataKey, serialized)

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
		p.TLSListenAddr = ""

		return nil
	case *net.TCPAddr:
		if peer.AuthInfo == nil {
			// no transport security being used
			addr, err := substituteListeningWithIP(p.ListenAddr, addr.IP.String())
			if err != nil {
				return fmt.Errorf("resolvePraefectAddress: for ListenAddr: %w", err)
			}

			p.ListenAddr = addr
			p.TLSListenAddr = ""
			p.SocketPath = ""

			return nil
		} else {
			authType := peer.AuthInfo.AuthType()
			if authType != (credentials.TLSInfo{}).AuthType() {
				return fmt.Errorf("resolvePraefectAddress: got TCP peer but with unknown transport security type %q", authType)
			}

			addr, err := substituteListeningWithIP(p.TLSListenAddr, addr.IP.String())
			if err != nil {
				return fmt.Errorf("resolvePraefectAddress: for TLSListenAddr: %w", err)
			}

			p.TLSListenAddr = addr
			p.ListenAddr = ""
			p.SocketPath = ""

			return nil
		}
	default:
		return fmt.Errorf("resolvePraefectAddress: unknown peer address scheme: %s", peer.Addr.Network())
	}
}

func substituteListeningWithIP(listenAddr, ip string) (string, error) {
	if listenAddr == "" {
		return "", errors.New("listening address is empty")
	}

	// We need to replace Praefect's IP address with the peer's
	// address as the value we have is from Praefect's configuration,
	// which may be a wildcard IP address ("0.0.0.0").
	listenURL, err := url.Parse(listenAddr)
	if err != nil {
		return "", fmt.Errorf("parse listening address %q: %w", listenAddr, err)
	}

	listenURL.Host = net.JoinHostPort(ip, listenURL.Port())
	return listenURL.String(), nil
}

// PraefectFromContext extracts `PraefectServer` from an incoming context. In
// case the metadata key is not set, the function will return `ErrPraefectServerNotFound`.
func PraefectFromContext(ctx context.Context) (*PraefectServer, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, ErrPraefectServerNotFound
	}

	serialized := md[PraefectMetadataKey]
	if len(serialized) == 0 {
		return nil, ErrPraefectServerNotFound
	}

	praefect, err := praefectFromSerialized(serialized[0])
	if err != nil {
		return nil, err
	}

	peer, ok := peer.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("PraefectFromContext: could not get peer")
	}

	if err := praefect.resolvePraefectAddress(peer); err != nil {
		return nil, err
	}

	return praefect, nil
}

func (p *PraefectServer) serialize() (string, error) {
	marshalled, err := json.Marshal(p)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(marshalled), nil
}

// praefectFromSerialized creates a Praefect server from a `serialize()`d string.
func praefectFromSerialized(serialized string) (*PraefectServer, error) {
	decoded, err := base64.StdEncoding.DecodeString(serialized)
	if err != nil {
		return nil, err
	}

	var server PraefectServer
	if err := json.Unmarshal(decoded, &server); err != nil {
		return nil, err
	}

	return &server, nil
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

	return praefectFromSerialized(praefectEnv)
}

// Env encodes the `PraefectServer` and returns an environment variable.
func (p *PraefectServer) Env() (string, error) {
	serialized, err := p.serialize()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s=%s", PraefectEnvKey, serialized), nil
}

func (p *PraefectServer) Address() (string, error) {
	for _, addr := range []string{p.SocketPath, p.TLSListenAddr, p.ListenAddr} {
		if addr != "" {
			return addr, nil
		}
	}

	return "", errors.New("no address configured")
}
