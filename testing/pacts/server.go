package pacts

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	// Update
)

// command-line options:
// gRPC server endpoint
var grpcServerEndpoint = flag.String("grpc-server-endpoint", "192.168.178.33:2305", "gRPC server endpoint")

func PactServerRun() error {
	log.Println(("Running grpc-server"))
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register gRPC server endpoint
	// Note: Make sure the gRPC server is running properly and accessible
	mux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if err := gitalypb.RegisterConflictsServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterSSHServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterServerServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterRepositoryServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterHookServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterDiffServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterObjectPoolServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterInternalGitalyHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterBlobServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterOperationServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterNamespaceServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterCleanupServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterWikiServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterSmartHTTPServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterRefTransactionHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterCommitServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterRefServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterRemoteServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}
	if err := gitalypb.RegisterPraefectInfoServiceHandlerFromEndpoint(ctx, mux, *grpcServerEndpoint, opts); err != nil {
		panic(err)
	}

	// Start HTTP server (and proxy calls to gRPC server endpoint)
	return http.ListenAndServe(":8081", mux)
}
