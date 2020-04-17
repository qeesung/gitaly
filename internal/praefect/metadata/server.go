package metadata

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"google.golang.org/grpc/metadata"
)

const (
	PraefectMetadataKey = "praefect-server"
	PraefectEnvKey      = "PRAEFECT_SERVER"
)

type PraefectServer struct {
	Address string `json:"address"`
	Token   string `json:"token"`
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
// case the metadata key is not set, the function will return `os.ErrNotExist`.
func ExtractPraefectServer(ctx context.Context) (p *PraefectServer, err error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, os.ErrNotExist
	}

	encoded := md[PraefectMetadataKey]
	if len(encoded) == 0 {
		return nil, os.ErrNotExist
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
// `os.ErrNotExist`.
func PraefectFromEnv() (*PraefectServer, error) {
	encoded, ok := os.LookupEnv(PraefectEnvKey)
	if !ok {
		return nil, os.ErrNotExist
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
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
