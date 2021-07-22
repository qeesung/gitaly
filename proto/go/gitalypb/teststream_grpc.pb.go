// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package gitalypb

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// TestStreamServiceClient is the client API for TestStreamService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type TestStreamServiceClient interface {
	TestStream(ctx context.Context, in *TestStreamRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
}

type testStreamServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewTestStreamServiceClient(cc grpc.ClientConnInterface) TestStreamServiceClient {
	return &testStreamServiceClient{cc}
}

func (c *testStreamServiceClient) TestStream(ctx context.Context, in *TestStreamRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/gitaly.TestStreamService/TestStream", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TestStreamServiceServer is the server API for TestStreamService service.
// All implementations must embed UnimplementedTestStreamServiceServer
// for forward compatibility
type TestStreamServiceServer interface {
	TestStream(context.Context, *TestStreamRequest) (*emptypb.Empty, error)
	mustEmbedUnimplementedTestStreamServiceServer()
}

// UnimplementedTestStreamServiceServer must be embedded to have forward compatible implementations.
type UnimplementedTestStreamServiceServer struct {
}

func (UnimplementedTestStreamServiceServer) TestStream(context.Context, *TestStreamRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method TestStream not implemented")
}
func (UnimplementedTestStreamServiceServer) mustEmbedUnimplementedTestStreamServiceServer() {}

// UnsafeTestStreamServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to TestStreamServiceServer will
// result in compilation errors.
type UnsafeTestStreamServiceServer interface {
	mustEmbedUnimplementedTestStreamServiceServer()
}

func RegisterTestStreamServiceServer(s grpc.ServiceRegistrar, srv TestStreamServiceServer) {
	s.RegisterService(&TestStreamService_ServiceDesc, srv)
}

func _TestStreamService_TestStream_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(TestStreamRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TestStreamServiceServer).TestStream(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.TestStreamService/TestStream",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TestStreamServiceServer).TestStream(ctx, req.(*TestStreamRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// TestStreamService_ServiceDesc is the grpc.ServiceDesc for TestStreamService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var TestStreamService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.TestStreamService",
	HandlerType: (*TestStreamServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "TestStream",
			Handler:    _TestStreamService_TestStream_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "teststream.proto",
}
