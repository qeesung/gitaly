package metadata

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
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
	// Address is the address of the Praefect server
	Address string `json:"address"`
	// Token is the token required to authenticate with the Praefect server
	Token string `json:"token"`
}

// InjectPraefectServer injects Praefect connection metadata into an incoming context
func InjectPraefectServer(ctx context.Context, conf config.Config) (context.Context, error) {
	var address string
	if conf.ListenAddr != "" {
		address = conf.ListenAddr
	} else if conf.SocketPath != "" {
		address = "unix://" + conf.SocketPath
	}

	praefectServer := PraefectServer{
		Address: address,
		Token:   conf.Auth.Token,
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

// ExtractPraefectServer extracts `PraefectServer` from an incoming context. In
// case the metadata key is not set, the function will return `ErrPraefectServerNotFound`.
func ExtractPraefectServer(ctx context.Context) (p *PraefectServer, err error) {
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
		return nil, fmt.Errorf("failed decoding base64: %v", err)
	}

	if err := json.Unmarshal(decoded, &p); err != nil {
		return nil, fmt.Errorf("failed unmarshalling json: %v", err)
	}

	return
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
func (p PraefectServer) Env() (string, error) {
	marshalled, err := json.Marshal(p)
	if err != nil {
		return "", err
	}

	encoded := base64.StdEncoding.EncodeToString(marshalled)
	return fmt.Sprintf("%s=%s", PraefectEnvKey, encoded), nil
}

// Dial will try to connect to the given Praefect server
func (p PraefectServer) Dial(ctx context.Context) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithBlock(),
	}
	if p.Token != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(p.Token)))
	}

	return client.DialContext(ctx, p.Address, opts)
}
