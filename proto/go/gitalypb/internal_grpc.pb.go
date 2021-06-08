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

// InternalGitalyClient is the client API for InternalGitaly service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type InternalGitalyClient interface {
	// WalkRepos walks the storage and streams back all known git repos on the
	// requested storage
	WalkRepos(ctx context.Context, in *WalkReposRequest, opts ...grpc.CallOption) (InternalGitaly_WalkReposClient, error)
}

type internalGitalyClient struct {
	cc grpc.ClientConnInterface
}

func NewInternalGitalyClient(cc grpc.ClientConnInterface) InternalGitalyClient {
	return &internalGitalyClient{cc}
}

func (c *internalGitalyClient) WalkRepos(ctx context.Context, in *WalkReposRequest, opts ...grpc.CallOption) (InternalGitaly_WalkReposClient, error) {
	stream, err := c.cc.NewStream(ctx, &InternalGitaly_ServiceDesc.Streams[0], "/gitaly.InternalGitaly/WalkRepos", opts...)
	if err != nil {
		return nil, err
	}
	x := &internalGitalyWalkReposClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type InternalGitaly_WalkReposClient interface {
	Recv() (*WalkReposResponse, error)
	grpc.ClientStream
}

type internalGitalyWalkReposClient struct {
	grpc.ClientStream
}

func (x *internalGitalyWalkReposClient) Recv() (*WalkReposResponse, error) {
	m := new(WalkReposResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// InternalGitalyServer is the server API for InternalGitaly service.
// All implementations must embed UnimplementedInternalGitalyServer
// for forward compatibility
type InternalGitalyServer interface {
	// WalkRepos walks the storage and streams back all known git repos on the
	// requested storage
	WalkRepos(*WalkReposRequest, InternalGitaly_WalkReposServer) error
	mustEmbedUnimplementedInternalGitalyServer()
}

// UnimplementedInternalGitalyServer must be embedded to have forward compatible implementations.
type UnimplementedInternalGitalyServer struct {
}

func (UnimplementedInternalGitalyServer) WalkRepos(*WalkReposRequest, InternalGitaly_WalkReposServer) error {
	return status.Errorf(codes.Unimplemented, "method WalkRepos not implemented")
}
func (UnimplementedInternalGitalyServer) mustEmbedUnimplementedInternalGitalyServer() {}

// UnsafeInternalGitalyServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to InternalGitalyServer will
// result in compilation errors.
type UnsafeInternalGitalyServer interface {
	mustEmbedUnimplementedInternalGitalyServer()
}

func RegisterInternalGitalyServer(s grpc.ServiceRegistrar, srv InternalGitalyServer) {
	s.RegisterService(&InternalGitaly_ServiceDesc, srv)
}

func _InternalGitaly_WalkRepos_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(WalkReposRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(InternalGitalyServer).WalkRepos(m, &internalGitalyWalkReposServer{stream})
}

type InternalGitaly_WalkReposServer interface {
	Send(*WalkReposResponse) error
	grpc.ServerStream
}

type internalGitalyWalkReposServer struct {
	grpc.ServerStream
}

func (x *internalGitalyWalkReposServer) Send(m *WalkReposResponse) error {
	return x.ServerStream.SendMsg(m)
}

// InternalGitaly_ServiceDesc is the grpc.ServiceDesc for InternalGitaly service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var InternalGitaly_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.InternalGitaly",
	HandlerType: (*InternalGitalyServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "WalkRepos",
			Handler:       _InternalGitaly_WalkRepos_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "internal.proto",
}
