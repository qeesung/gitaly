package helper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// ExtractGitalyServers extracts `storage.GitalyServers` from an incoming context.
func ExtractGitalyServers(ctx context.Context) (gitalyServersInfo storage.GitalyServers, err error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("empty metadata")
	}

	gitalyServersJSONEncoded := md["gitaly-servers"]
	if len(gitalyServersJSONEncoded) == 0 {
		return nil, fmt.Errorf("empty gitaly-servers metadata")
	}

	gitalyServersJSON, err := base64.StdEncoding.DecodeString(gitalyServersJSONEncoded[0])
	if err != nil {
		return nil, fmt.Errorf("failed decoding base64: %v", err)
	}

	if err := json.Unmarshal(gitalyServersJSON, &gitalyServersInfo); err != nil {
		return nil, fmt.Errorf("failed unmarshalling json: %v", err)
	}

	return
}

// IncomingToOutgoing creates an outgoing context out of an incoming context with the same storage metadata
func IncomingToOutgoing(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}

	return metadata.NewOutgoingContext(ctx, md)
}

// InjectGitalyServers injects gitaly-servers metadata into an outgoing context
func InjectGitalyServers(ctx context.Context, name, address, token string) (context.Context, error) {
	gitalyServers := storage.GitalyServers{
		name: {
			"address": address,
			"token":   token,
		},
	}

	gitalyServersJSON, err := json.Marshal(gitalyServers)
	if err != nil {
		return nil, err
	}

	return metadata.NewOutgoingContext(ctx, metadata.Pairs("gitaly-servers", base64.StdEncoding.EncodeToString(gitalyServersJSON))), nil
}

// ClientConnection creates a grpc.ClientConn from the injected gitaly-servers metadata
func ClientConnection(ctx context.Context, storageName string) (*grpc.ClientConn, error) {
	gitalyServersInfo, err := ExtractGitalyServers(ctx)
	if err != nil {
		return nil, err
	}

	repoStorageInfo, ok := gitalyServersInfo[storageName]
	if !ok {
		return nil, fmt.Errorf("gitaly server info for %q not found", storageName)
	}

	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentials(repoStorageInfo["token"])),
	}

	conn, err := client.Dial(repoStorageInfo["address"], connOpts)
	if err != nil {
		return nil, fmt.Errorf("could not dial source: %v", err)
	}

	return conn, nil
}
