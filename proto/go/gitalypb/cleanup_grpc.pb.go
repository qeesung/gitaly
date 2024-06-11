// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.4.0
// - protoc             v4.23.1
// source: cleanup.proto

package gitalypb

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.62.0 or later.
const _ = grpc.SupportPackageIsVersion8

const (
	CleanupService_ApplyBfgObjectMapStream_FullMethodName = "/gitaly.CleanupService/ApplyBfgObjectMapStream"
	CleanupService_RewriteHistory_FullMethodName          = "/gitaly.CleanupService/RewriteHistory"
)

// CleanupServiceClient is the client API for CleanupService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// CleanupService provides RPCs to clean up a repository's contents.
type CleanupServiceClient interface {
	// ApplyBfgObjectMapStream ...
	ApplyBfgObjectMapStream(ctx context.Context, opts ...grpc.CallOption) (CleanupService_ApplyBfgObjectMapStreamClient, error)
	// RewriteHistory redacts targeted strings and deletes requested blobs in a
	// repository and updates all references to point to the rewritten commit
	// history. This is useful for removing inadvertently pushed secrets from your
	// repository and purging large blobs. This is a dangerous operation.
	//
	// The following known error conditions may happen:
	//
	// - `InvalidArgument` in the following situations:
	//   - The provided repository can't be validated.
	//   - The repository field is set on any request other than the initial one.
	//   - All of the client requests do not contain either blobs to remove or
	//     redaction patterns to redact.
	//   - A blob object ID is invalid.
	//   - A redaction pattern contains a newline character.
	//
	// - `Aborted` if the repository is mutated while this RPC is executing.
	RewriteHistory(ctx context.Context, opts ...grpc.CallOption) (CleanupService_RewriteHistoryClient, error)
}

type cleanupServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewCleanupServiceClient(cc grpc.ClientConnInterface) CleanupServiceClient {
	return &cleanupServiceClient{cc}
}

func (c *cleanupServiceClient) ApplyBfgObjectMapStream(ctx context.Context, opts ...grpc.CallOption) (CleanupService_ApplyBfgObjectMapStreamClient, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &CleanupService_ServiceDesc.Streams[0], CleanupService_ApplyBfgObjectMapStream_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &cleanupServiceApplyBfgObjectMapStreamClient{ClientStream: stream}
	return x, nil
}

type CleanupService_ApplyBfgObjectMapStreamClient interface {
	Send(*ApplyBfgObjectMapStreamRequest) error
	Recv() (*ApplyBfgObjectMapStreamResponse, error)
	grpc.ClientStream
}

type cleanupServiceApplyBfgObjectMapStreamClient struct {
	grpc.ClientStream
}

func (x *cleanupServiceApplyBfgObjectMapStreamClient) Send(m *ApplyBfgObjectMapStreamRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *cleanupServiceApplyBfgObjectMapStreamClient) Recv() (*ApplyBfgObjectMapStreamResponse, error) {
	m := new(ApplyBfgObjectMapStreamResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *cleanupServiceClient) RewriteHistory(ctx context.Context, opts ...grpc.CallOption) (CleanupService_RewriteHistoryClient, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &CleanupService_ServiceDesc.Streams[1], CleanupService_RewriteHistory_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &cleanupServiceRewriteHistoryClient{ClientStream: stream}
	return x, nil
}

type CleanupService_RewriteHistoryClient interface {
	Send(*RewriteHistoryRequest) error
	CloseAndRecv() (*RewriteHistoryResponse, error)
	grpc.ClientStream
}

type cleanupServiceRewriteHistoryClient struct {
	grpc.ClientStream
}

func (x *cleanupServiceRewriteHistoryClient) Send(m *RewriteHistoryRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *cleanupServiceRewriteHistoryClient) CloseAndRecv() (*RewriteHistoryResponse, error) {
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	m := new(RewriteHistoryResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// CleanupServiceServer is the server API for CleanupService service.
// All implementations must embed UnimplementedCleanupServiceServer
// for forward compatibility
//
// CleanupService provides RPCs to clean up a repository's contents.
type CleanupServiceServer interface {
	// ApplyBfgObjectMapStream ...
	ApplyBfgObjectMapStream(CleanupService_ApplyBfgObjectMapStreamServer) error
	// RewriteHistory redacts targeted strings and deletes requested blobs in a
	// repository and updates all references to point to the rewritten commit
	// history. This is useful for removing inadvertently pushed secrets from your
	// repository and purging large blobs. This is a dangerous operation.
	//
	// The following known error conditions may happen:
	//
	// - `InvalidArgument` in the following situations:
	//   - The provided repository can't be validated.
	//   - The repository field is set on any request other than the initial one.
	//   - All of the client requests do not contain either blobs to remove or
	//     redaction patterns to redact.
	//   - A blob object ID is invalid.
	//   - A redaction pattern contains a newline character.
	//
	// - `Aborted` if the repository is mutated while this RPC is executing.
	RewriteHistory(CleanupService_RewriteHistoryServer) error
	mustEmbedUnimplementedCleanupServiceServer()
}

// UnimplementedCleanupServiceServer must be embedded to have forward compatible implementations.
type UnimplementedCleanupServiceServer struct {
}

func (UnimplementedCleanupServiceServer) ApplyBfgObjectMapStream(CleanupService_ApplyBfgObjectMapStreamServer) error {
	return status.Errorf(codes.Unimplemented, "method ApplyBfgObjectMapStream not implemented")
}
func (UnimplementedCleanupServiceServer) RewriteHistory(CleanupService_RewriteHistoryServer) error {
	return status.Errorf(codes.Unimplemented, "method RewriteHistory not implemented")
}
func (UnimplementedCleanupServiceServer) mustEmbedUnimplementedCleanupServiceServer() {}

// UnsafeCleanupServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to CleanupServiceServer will
// result in compilation errors.
type UnsafeCleanupServiceServer interface {
	mustEmbedUnimplementedCleanupServiceServer()
}

func RegisterCleanupServiceServer(s grpc.ServiceRegistrar, srv CleanupServiceServer) {
	s.RegisterService(&CleanupService_ServiceDesc, srv)
}

func _CleanupService_ApplyBfgObjectMapStream_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(CleanupServiceServer).ApplyBfgObjectMapStream(&cleanupServiceApplyBfgObjectMapStreamServer{ServerStream: stream})
}

type CleanupService_ApplyBfgObjectMapStreamServer interface {
	Send(*ApplyBfgObjectMapStreamResponse) error
	Recv() (*ApplyBfgObjectMapStreamRequest, error)
	grpc.ServerStream
}

type cleanupServiceApplyBfgObjectMapStreamServer struct {
	grpc.ServerStream
}

func (x *cleanupServiceApplyBfgObjectMapStreamServer) Send(m *ApplyBfgObjectMapStreamResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *cleanupServiceApplyBfgObjectMapStreamServer) Recv() (*ApplyBfgObjectMapStreamRequest, error) {
	m := new(ApplyBfgObjectMapStreamRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func _CleanupService_RewriteHistory_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(CleanupServiceServer).RewriteHistory(&cleanupServiceRewriteHistoryServer{ServerStream: stream})
}

type CleanupService_RewriteHistoryServer interface {
	SendAndClose(*RewriteHistoryResponse) error
	Recv() (*RewriteHistoryRequest, error)
	grpc.ServerStream
}

type cleanupServiceRewriteHistoryServer struct {
	grpc.ServerStream
}

func (x *cleanupServiceRewriteHistoryServer) SendAndClose(m *RewriteHistoryResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *cleanupServiceRewriteHistoryServer) Recv() (*RewriteHistoryRequest, error) {
	m := new(RewriteHistoryRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// CleanupService_ServiceDesc is the grpc.ServiceDesc for CleanupService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var CleanupService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.CleanupService",
	HandlerType: (*CleanupServiceServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "ApplyBfgObjectMapStream",
			Handler:       _CleanupService_ApplyBfgObjectMapStream_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "RewriteHistory",
			Handler:       _CleanupService_RewriteHistory_Handler,
			ClientStreams: true,
		},
	},
	Metadata: "cleanup.proto",
}
