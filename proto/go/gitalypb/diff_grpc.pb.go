// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package gitalypb

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// DiffServiceClient is the client API for DiffService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type DiffServiceClient interface {
	// Returns stream of CommitDiffResponse with patches chunked over messages
	CommitDiff(ctx context.Context, in *CommitDiffRequest, opts ...grpc.CallOption) (DiffService_CommitDiffClient, error)
	// Return a stream so we can divide the response in chunks of deltas
	CommitDelta(ctx context.Context, in *CommitDeltaRequest, opts ...grpc.CallOption) (DiffService_CommitDeltaClient, error)
	// This comment is left unintentionally blank.
	RawDiff(ctx context.Context, in *RawDiffRequest, opts ...grpc.CallOption) (DiffService_RawDiffClient, error)
	// This comment is left unintentionally blank.
	RawPatch(ctx context.Context, in *RawPatchRequest, opts ...grpc.CallOption) (DiffService_RawPatchClient, error)
	// This comment is left unintentionally blank.
	DiffStats(ctx context.Context, in *DiffStatsRequest, opts ...grpc.CallOption) (DiffService_DiffStatsClient, error)
	// Return a list of files changed along with the status of each file
	FindChangedPaths(ctx context.Context, in *FindChangedPathsRequest, opts ...grpc.CallOption) (DiffService_FindChangedPathsClient, error)
	// Return a list of files changed between commits along with the status of each file
	FindChangedPathsBetweenCommits(ctx context.Context, in *FindChangedPathsBetweenCommitsRequest, opts ...grpc.CallOption) (DiffService_FindChangedPathsBetweenCommitsClient, error)
}

type diffServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewDiffServiceClient(cc grpc.ClientConnInterface) DiffServiceClient {
	return &diffServiceClient{cc}
}

func (c *diffServiceClient) CommitDiff(ctx context.Context, in *CommitDiffRequest, opts ...grpc.CallOption) (DiffService_CommitDiffClient, error) {
	stream, err := c.cc.NewStream(ctx, &DiffService_ServiceDesc.Streams[0], "/gitaly.DiffService/CommitDiff", opts...)
	if err != nil {
		return nil, err
	}
	x := &diffServiceCommitDiffClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type DiffService_CommitDiffClient interface {
	Recv() (*CommitDiffResponse, error)
	grpc.ClientStream
}

type diffServiceCommitDiffClient struct {
	grpc.ClientStream
}

func (x *diffServiceCommitDiffClient) Recv() (*CommitDiffResponse, error) {
	m := new(CommitDiffResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *diffServiceClient) CommitDelta(ctx context.Context, in *CommitDeltaRequest, opts ...grpc.CallOption) (DiffService_CommitDeltaClient, error) {
	stream, err := c.cc.NewStream(ctx, &DiffService_ServiceDesc.Streams[1], "/gitaly.DiffService/CommitDelta", opts...)
	if err != nil {
		return nil, err
	}
	x := &diffServiceCommitDeltaClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type DiffService_CommitDeltaClient interface {
	Recv() (*CommitDeltaResponse, error)
	grpc.ClientStream
}

type diffServiceCommitDeltaClient struct {
	grpc.ClientStream
}

func (x *diffServiceCommitDeltaClient) Recv() (*CommitDeltaResponse, error) {
	m := new(CommitDeltaResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *diffServiceClient) RawDiff(ctx context.Context, in *RawDiffRequest, opts ...grpc.CallOption) (DiffService_RawDiffClient, error) {
	stream, err := c.cc.NewStream(ctx, &DiffService_ServiceDesc.Streams[2], "/gitaly.DiffService/RawDiff", opts...)
	if err != nil {
		return nil, err
	}
	x := &diffServiceRawDiffClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type DiffService_RawDiffClient interface {
	Recv() (*RawDiffResponse, error)
	grpc.ClientStream
}

type diffServiceRawDiffClient struct {
	grpc.ClientStream
}

func (x *diffServiceRawDiffClient) Recv() (*RawDiffResponse, error) {
	m := new(RawDiffResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *diffServiceClient) RawPatch(ctx context.Context, in *RawPatchRequest, opts ...grpc.CallOption) (DiffService_RawPatchClient, error) {
	stream, err := c.cc.NewStream(ctx, &DiffService_ServiceDesc.Streams[3], "/gitaly.DiffService/RawPatch", opts...)
	if err != nil {
		return nil, err
	}
	x := &diffServiceRawPatchClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type DiffService_RawPatchClient interface {
	Recv() (*RawPatchResponse, error)
	grpc.ClientStream
}

type diffServiceRawPatchClient struct {
	grpc.ClientStream
}

func (x *diffServiceRawPatchClient) Recv() (*RawPatchResponse, error) {
	m := new(RawPatchResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *diffServiceClient) DiffStats(ctx context.Context, in *DiffStatsRequest, opts ...grpc.CallOption) (DiffService_DiffStatsClient, error) {
	stream, err := c.cc.NewStream(ctx, &DiffService_ServiceDesc.Streams[4], "/gitaly.DiffService/DiffStats", opts...)
	if err != nil {
		return nil, err
	}
	x := &diffServiceDiffStatsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type DiffService_DiffStatsClient interface {
	Recv() (*DiffStatsResponse, error)
	grpc.ClientStream
}

type diffServiceDiffStatsClient struct {
	grpc.ClientStream
}

func (x *diffServiceDiffStatsClient) Recv() (*DiffStatsResponse, error) {
	m := new(DiffStatsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *diffServiceClient) FindChangedPaths(ctx context.Context, in *FindChangedPathsRequest, opts ...grpc.CallOption) (DiffService_FindChangedPathsClient, error) {
	stream, err := c.cc.NewStream(ctx, &DiffService_ServiceDesc.Streams[5], "/gitaly.DiffService/FindChangedPaths", opts...)
	if err != nil {
		return nil, err
	}
	x := &diffServiceFindChangedPathsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type DiffService_FindChangedPathsClient interface {
	Recv() (*FindChangedPathsResponse, error)
	grpc.ClientStream
}

type diffServiceFindChangedPathsClient struct {
	grpc.ClientStream
}

func (x *diffServiceFindChangedPathsClient) Recv() (*FindChangedPathsResponse, error) {
	m := new(FindChangedPathsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *diffServiceClient) FindChangedPathsBetweenCommits(ctx context.Context, in *FindChangedPathsBetweenCommitsRequest, opts ...grpc.CallOption) (DiffService_FindChangedPathsBetweenCommitsClient, error) {
	stream, err := c.cc.NewStream(ctx, &DiffService_ServiceDesc.Streams[6], "/gitaly.DiffService/FindChangedPathsBetweenCommits", opts...)
	if err != nil {
		return nil, err
	}
	x := &diffServiceFindChangedPathsBetweenCommitsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type DiffService_FindChangedPathsBetweenCommitsClient interface {
	Recv() (*FindChangedPathsBetweenCommitsResponse, error)
	grpc.ClientStream
}

type diffServiceFindChangedPathsBetweenCommitsClient struct {
	grpc.ClientStream
}

func (x *diffServiceFindChangedPathsBetweenCommitsClient) Recv() (*FindChangedPathsBetweenCommitsResponse, error) {
	m := new(FindChangedPathsBetweenCommitsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// DiffServiceServer is the server API for DiffService service.
// All implementations must embed UnimplementedDiffServiceServer
// for forward compatibility
type DiffServiceServer interface {
	// Returns stream of CommitDiffResponse with patches chunked over messages
	CommitDiff(*CommitDiffRequest, DiffService_CommitDiffServer) error
	// Return a stream so we can divide the response in chunks of deltas
	CommitDelta(*CommitDeltaRequest, DiffService_CommitDeltaServer) error
	// This comment is left unintentionally blank.
	RawDiff(*RawDiffRequest, DiffService_RawDiffServer) error
	// This comment is left unintentionally blank.
	RawPatch(*RawPatchRequest, DiffService_RawPatchServer) error
	// This comment is left unintentionally blank.
	DiffStats(*DiffStatsRequest, DiffService_DiffStatsServer) error
	// Return a list of files changed along with the status of each file
	FindChangedPaths(*FindChangedPathsRequest, DiffService_FindChangedPathsServer) error
	// Return a list of files changed between commits along with the status of each file
	FindChangedPathsBetweenCommits(*FindChangedPathsBetweenCommitsRequest, DiffService_FindChangedPathsBetweenCommitsServer) error
	mustEmbedUnimplementedDiffServiceServer()
}

// UnimplementedDiffServiceServer must be embedded to have forward compatible implementations.
type UnimplementedDiffServiceServer struct {
}

func (UnimplementedDiffServiceServer) CommitDiff(*CommitDiffRequest, DiffService_CommitDiffServer) error {
	return status.Errorf(codes.Unimplemented, "method CommitDiff not implemented")
}
func (UnimplementedDiffServiceServer) CommitDelta(*CommitDeltaRequest, DiffService_CommitDeltaServer) error {
	return status.Errorf(codes.Unimplemented, "method CommitDelta not implemented")
}
func (UnimplementedDiffServiceServer) RawDiff(*RawDiffRequest, DiffService_RawDiffServer) error {
	return status.Errorf(codes.Unimplemented, "method RawDiff not implemented")
}
func (UnimplementedDiffServiceServer) RawPatch(*RawPatchRequest, DiffService_RawPatchServer) error {
	return status.Errorf(codes.Unimplemented, "method RawPatch not implemented")
}
func (UnimplementedDiffServiceServer) DiffStats(*DiffStatsRequest, DiffService_DiffStatsServer) error {
	return status.Errorf(codes.Unimplemented, "method DiffStats not implemented")
}
func (UnimplementedDiffServiceServer) FindChangedPaths(*FindChangedPathsRequest, DiffService_FindChangedPathsServer) error {
	return status.Errorf(codes.Unimplemented, "method FindChangedPaths not implemented")
}
func (UnimplementedDiffServiceServer) FindChangedPathsBetweenCommits(*FindChangedPathsBetweenCommitsRequest, DiffService_FindChangedPathsBetweenCommitsServer) error {
	return status.Errorf(codes.Unimplemented, "method FindChangedPathsBetweenCommits not implemented")
}
func (UnimplementedDiffServiceServer) mustEmbedUnimplementedDiffServiceServer() {}

// UnsafeDiffServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to DiffServiceServer will
// result in compilation errors.
type UnsafeDiffServiceServer interface {
	mustEmbedUnimplementedDiffServiceServer()
}

func RegisterDiffServiceServer(s grpc.ServiceRegistrar, srv DiffServiceServer) {
	s.RegisterService(&DiffService_ServiceDesc, srv)
}

func _DiffService_CommitDiff_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(CommitDiffRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(DiffServiceServer).CommitDiff(m, &diffServiceCommitDiffServer{stream})
}

type DiffService_CommitDiffServer interface {
	Send(*CommitDiffResponse) error
	grpc.ServerStream
}

type diffServiceCommitDiffServer struct {
	grpc.ServerStream
}

func (x *diffServiceCommitDiffServer) Send(m *CommitDiffResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _DiffService_CommitDelta_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(CommitDeltaRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(DiffServiceServer).CommitDelta(m, &diffServiceCommitDeltaServer{stream})
}

type DiffService_CommitDeltaServer interface {
	Send(*CommitDeltaResponse) error
	grpc.ServerStream
}

type diffServiceCommitDeltaServer struct {
	grpc.ServerStream
}

func (x *diffServiceCommitDeltaServer) Send(m *CommitDeltaResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _DiffService_RawDiff_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(RawDiffRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(DiffServiceServer).RawDiff(m, &diffServiceRawDiffServer{stream})
}

type DiffService_RawDiffServer interface {
	Send(*RawDiffResponse) error
	grpc.ServerStream
}

type diffServiceRawDiffServer struct {
	grpc.ServerStream
}

func (x *diffServiceRawDiffServer) Send(m *RawDiffResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _DiffService_RawPatch_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(RawPatchRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(DiffServiceServer).RawPatch(m, &diffServiceRawPatchServer{stream})
}

type DiffService_RawPatchServer interface {
	Send(*RawPatchResponse) error
	grpc.ServerStream
}

type diffServiceRawPatchServer struct {
	grpc.ServerStream
}

func (x *diffServiceRawPatchServer) Send(m *RawPatchResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _DiffService_DiffStats_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(DiffStatsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(DiffServiceServer).DiffStats(m, &diffServiceDiffStatsServer{stream})
}

type DiffService_DiffStatsServer interface {
	Send(*DiffStatsResponse) error
	grpc.ServerStream
}

type diffServiceDiffStatsServer struct {
	grpc.ServerStream
}

func (x *diffServiceDiffStatsServer) Send(m *DiffStatsResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _DiffService_FindChangedPaths_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(FindChangedPathsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(DiffServiceServer).FindChangedPaths(m, &diffServiceFindChangedPathsServer{stream})
}

type DiffService_FindChangedPathsServer interface {
	Send(*FindChangedPathsResponse) error
	grpc.ServerStream
}

type diffServiceFindChangedPathsServer struct {
	grpc.ServerStream
}

func (x *diffServiceFindChangedPathsServer) Send(m *FindChangedPathsResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _DiffService_FindChangedPathsBetweenCommits_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(FindChangedPathsBetweenCommitsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(DiffServiceServer).FindChangedPathsBetweenCommits(m, &diffServiceFindChangedPathsBetweenCommitsServer{stream})
}

type DiffService_FindChangedPathsBetweenCommitsServer interface {
	Send(*FindChangedPathsBetweenCommitsResponse) error
	grpc.ServerStream
}

type diffServiceFindChangedPathsBetweenCommitsServer struct {
	grpc.ServerStream
}

func (x *diffServiceFindChangedPathsBetweenCommitsServer) Send(m *FindChangedPathsBetweenCommitsResponse) error {
	return x.ServerStream.SendMsg(m)
}

// DiffService_ServiceDesc is the grpc.ServiceDesc for DiffService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var DiffService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.DiffService",
	HandlerType: (*DiffServiceServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "CommitDiff",
			Handler:       _DiffService_CommitDiff_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "CommitDelta",
			Handler:       _DiffService_CommitDelta_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "RawDiff",
			Handler:       _DiffService_RawDiff_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "RawPatch",
			Handler:       _DiffService_RawPatch_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "DiffStats",
			Handler:       _DiffService_DiffStats_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "FindChangedPaths",
			Handler:       _DiffService_FindChangedPaths_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "FindChangedPathsBetweenCommits",
			Handler:       _DiffService_FindChangedPathsBetweenCommits_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "diff.proto",
}
