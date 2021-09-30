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

// SmartHTTPServiceClient is the client API for SmartHTTPService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type SmartHTTPServiceClient interface {
	// The response body for GET /info/refs?service=git-upload-pack
	// Will be invoked when the user executes a `git fetch`, meaning the server
	// will upload the packs to that user. The user doesn't upload new objects.
	InfoRefsUploadPack(ctx context.Context, in *InfoRefsRequest, opts ...grpc.CallOption) (SmartHTTPService_InfoRefsUploadPackClient, error)
	// The response body for GET /info/refs?service=git-receive-pack
	// Will be invoked when the user executes a `git push`, but only advertises
	// references to the user.
	InfoRefsReceivePack(ctx context.Context, in *InfoRefsRequest, opts ...grpc.CallOption) (SmartHTTPService_InfoRefsReceivePackClient, error)
	// Request and response body for POST /upload-pack
	PostUploadPack(ctx context.Context, opts ...grpc.CallOption) (SmartHTTPService_PostUploadPackClient, error)
	// Request and response body for POST /upload-pack using sidechannel protocol
	PostUploadPackWithSidechannel(ctx context.Context, in *PostUploadPackWithSidechannelRequest, opts ...grpc.CallOption) (*PostUploadPackWithSidechannelResponse, error)
	// Request and response body for POST /receive-pack
	PostReceivePack(ctx context.Context, opts ...grpc.CallOption) (SmartHTTPService_PostReceivePackClient, error)
}

type smartHTTPServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewSmartHTTPServiceClient(cc grpc.ClientConnInterface) SmartHTTPServiceClient {
	return &smartHTTPServiceClient{cc}
}

func (c *smartHTTPServiceClient) InfoRefsUploadPack(ctx context.Context, in *InfoRefsRequest, opts ...grpc.CallOption) (SmartHTTPService_InfoRefsUploadPackClient, error) {
	stream, err := c.cc.NewStream(ctx, &SmartHTTPService_ServiceDesc.Streams[0], "/gitaly.SmartHTTPService/InfoRefsUploadPack", opts...)
	if err != nil {
		return nil, err
	}
	x := &smartHTTPServiceInfoRefsUploadPackClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type SmartHTTPService_InfoRefsUploadPackClient interface {
	Recv() (*InfoRefsResponse, error)
	grpc.ClientStream
}

type smartHTTPServiceInfoRefsUploadPackClient struct {
	grpc.ClientStream
}

func (x *smartHTTPServiceInfoRefsUploadPackClient) Recv() (*InfoRefsResponse, error) {
	m := new(InfoRefsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *smartHTTPServiceClient) InfoRefsReceivePack(ctx context.Context, in *InfoRefsRequest, opts ...grpc.CallOption) (SmartHTTPService_InfoRefsReceivePackClient, error) {
	stream, err := c.cc.NewStream(ctx, &SmartHTTPService_ServiceDesc.Streams[1], "/gitaly.SmartHTTPService/InfoRefsReceivePack", opts...)
	if err != nil {
		return nil, err
	}
	x := &smartHTTPServiceInfoRefsReceivePackClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type SmartHTTPService_InfoRefsReceivePackClient interface {
	Recv() (*InfoRefsResponse, error)
	grpc.ClientStream
}

type smartHTTPServiceInfoRefsReceivePackClient struct {
	grpc.ClientStream
}

func (x *smartHTTPServiceInfoRefsReceivePackClient) Recv() (*InfoRefsResponse, error) {
	m := new(InfoRefsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *smartHTTPServiceClient) PostUploadPack(ctx context.Context, opts ...grpc.CallOption) (SmartHTTPService_PostUploadPackClient, error) {
	stream, err := c.cc.NewStream(ctx, &SmartHTTPService_ServiceDesc.Streams[2], "/gitaly.SmartHTTPService/PostUploadPack", opts...)
	if err != nil {
		return nil, err
	}
	x := &smartHTTPServicePostUploadPackClient{stream}
	return x, nil
}

type SmartHTTPService_PostUploadPackClient interface {
	Send(*PostUploadPackRequest) error
	Recv() (*PostUploadPackResponse, error)
	grpc.ClientStream
}

type smartHTTPServicePostUploadPackClient struct {
	grpc.ClientStream
}

func (x *smartHTTPServicePostUploadPackClient) Send(m *PostUploadPackRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *smartHTTPServicePostUploadPackClient) Recv() (*PostUploadPackResponse, error) {
	m := new(PostUploadPackResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *smartHTTPServiceClient) PostUploadPackWithSidechannel(ctx context.Context, in *PostUploadPackWithSidechannelRequest, opts ...grpc.CallOption) (*PostUploadPackWithSidechannelResponse, error) {
	out := new(PostUploadPackWithSidechannelResponse)
	err := c.cc.Invoke(ctx, "/gitaly.SmartHTTPService/PostUploadPackWithSidechannel", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *smartHTTPServiceClient) PostReceivePack(ctx context.Context, opts ...grpc.CallOption) (SmartHTTPService_PostReceivePackClient, error) {
	stream, err := c.cc.NewStream(ctx, &SmartHTTPService_ServiceDesc.Streams[3], "/gitaly.SmartHTTPService/PostReceivePack", opts...)
	if err != nil {
		return nil, err
	}
	x := &smartHTTPServicePostReceivePackClient{stream}
	return x, nil
}

type SmartHTTPService_PostReceivePackClient interface {
	Send(*PostReceivePackRequest) error
	Recv() (*PostReceivePackResponse, error)
	grpc.ClientStream
}

type smartHTTPServicePostReceivePackClient struct {
	grpc.ClientStream
}

func (x *smartHTTPServicePostReceivePackClient) Send(m *PostReceivePackRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *smartHTTPServicePostReceivePackClient) Recv() (*PostReceivePackResponse, error) {
	m := new(PostReceivePackResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// SmartHTTPServiceServer is the server API for SmartHTTPService service.
// All implementations must embed UnimplementedSmartHTTPServiceServer
// for forward compatibility
type SmartHTTPServiceServer interface {
	// The response body for GET /info/refs?service=git-upload-pack
	// Will be invoked when the user executes a `git fetch`, meaning the server
	// will upload the packs to that user. The user doesn't upload new objects.
	InfoRefsUploadPack(*InfoRefsRequest, SmartHTTPService_InfoRefsUploadPackServer) error
	// The response body for GET /info/refs?service=git-receive-pack
	// Will be invoked when the user executes a `git push`, but only advertises
	// references to the user.
	InfoRefsReceivePack(*InfoRefsRequest, SmartHTTPService_InfoRefsReceivePackServer) error
	// Request and response body for POST /upload-pack
	PostUploadPack(SmartHTTPService_PostUploadPackServer) error
	// Request and response body for POST /upload-pack using sidechannel protocol
	PostUploadPackWithSidechannel(context.Context, *PostUploadPackWithSidechannelRequest) (*PostUploadPackWithSidechannelResponse, error)
	// Request and response body for POST /receive-pack
	PostReceivePack(SmartHTTPService_PostReceivePackServer) error
	mustEmbedUnimplementedSmartHTTPServiceServer()
}

// UnimplementedSmartHTTPServiceServer must be embedded to have forward compatible implementations.
type UnimplementedSmartHTTPServiceServer struct {
}

func (UnimplementedSmartHTTPServiceServer) InfoRefsUploadPack(*InfoRefsRequest, SmartHTTPService_InfoRefsUploadPackServer) error {
	return status.Errorf(codes.Unimplemented, "method InfoRefsUploadPack not implemented")
}
func (UnimplementedSmartHTTPServiceServer) InfoRefsReceivePack(*InfoRefsRequest, SmartHTTPService_InfoRefsReceivePackServer) error {
	return status.Errorf(codes.Unimplemented, "method InfoRefsReceivePack not implemented")
}
func (UnimplementedSmartHTTPServiceServer) PostUploadPack(SmartHTTPService_PostUploadPackServer) error {
	return status.Errorf(codes.Unimplemented, "method PostUploadPack not implemented")
}
func (UnimplementedSmartHTTPServiceServer) PostUploadPackWithSidechannel(context.Context, *PostUploadPackWithSidechannelRequest) (*PostUploadPackWithSidechannelResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method PostUploadPackWithSidechannel not implemented")
}
func (UnimplementedSmartHTTPServiceServer) PostReceivePack(SmartHTTPService_PostReceivePackServer) error {
	return status.Errorf(codes.Unimplemented, "method PostReceivePack not implemented")
}
func (UnimplementedSmartHTTPServiceServer) mustEmbedUnimplementedSmartHTTPServiceServer() {}

// UnsafeSmartHTTPServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to SmartHTTPServiceServer will
// result in compilation errors.
type UnsafeSmartHTTPServiceServer interface {
	mustEmbedUnimplementedSmartHTTPServiceServer()
}

func RegisterSmartHTTPServiceServer(s grpc.ServiceRegistrar, srv SmartHTTPServiceServer) {
	s.RegisterService(&SmartHTTPService_ServiceDesc, srv)
}

func _SmartHTTPService_InfoRefsUploadPack_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(InfoRefsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(SmartHTTPServiceServer).InfoRefsUploadPack(m, &smartHTTPServiceInfoRefsUploadPackServer{stream})
}

type SmartHTTPService_InfoRefsUploadPackServer interface {
	Send(*InfoRefsResponse) error
	grpc.ServerStream
}

type smartHTTPServiceInfoRefsUploadPackServer struct {
	grpc.ServerStream
}

func (x *smartHTTPServiceInfoRefsUploadPackServer) Send(m *InfoRefsResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _SmartHTTPService_InfoRefsReceivePack_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(InfoRefsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(SmartHTTPServiceServer).InfoRefsReceivePack(m, &smartHTTPServiceInfoRefsReceivePackServer{stream})
}

type SmartHTTPService_InfoRefsReceivePackServer interface {
	Send(*InfoRefsResponse) error
	grpc.ServerStream
}

type smartHTTPServiceInfoRefsReceivePackServer struct {
	grpc.ServerStream
}

func (x *smartHTTPServiceInfoRefsReceivePackServer) Send(m *InfoRefsResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _SmartHTTPService_PostUploadPack_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(SmartHTTPServiceServer).PostUploadPack(&smartHTTPServicePostUploadPackServer{stream})
}

type SmartHTTPService_PostUploadPackServer interface {
	Send(*PostUploadPackResponse) error
	Recv() (*PostUploadPackRequest, error)
	grpc.ServerStream
}

type smartHTTPServicePostUploadPackServer struct {
	grpc.ServerStream
}

func (x *smartHTTPServicePostUploadPackServer) Send(m *PostUploadPackResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *smartHTTPServicePostUploadPackServer) Recv() (*PostUploadPackRequest, error) {
	m := new(PostUploadPackRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func _SmartHTTPService_PostUploadPackWithSidechannel_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PostUploadPackWithSidechannelRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SmartHTTPServiceServer).PostUploadPackWithSidechannel(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.SmartHTTPService/PostUploadPackWithSidechannel",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SmartHTTPServiceServer).PostUploadPackWithSidechannel(ctx, req.(*PostUploadPackWithSidechannelRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _SmartHTTPService_PostReceivePack_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(SmartHTTPServiceServer).PostReceivePack(&smartHTTPServicePostReceivePackServer{stream})
}

type SmartHTTPService_PostReceivePackServer interface {
	Send(*PostReceivePackResponse) error
	Recv() (*PostReceivePackRequest, error)
	grpc.ServerStream
}

type smartHTTPServicePostReceivePackServer struct {
	grpc.ServerStream
}

func (x *smartHTTPServicePostReceivePackServer) Send(m *PostReceivePackResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *smartHTTPServicePostReceivePackServer) Recv() (*PostReceivePackRequest, error) {
	m := new(PostReceivePackRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// SmartHTTPService_ServiceDesc is the grpc.ServiceDesc for SmartHTTPService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var SmartHTTPService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.SmartHTTPService",
	HandlerType: (*SmartHTTPServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "PostUploadPackWithSidechannel",
			Handler:    _SmartHTTPService_PostUploadPackWithSidechannel_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "InfoRefsUploadPack",
			Handler:       _SmartHTTPService_InfoRefsUploadPack_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "InfoRefsReceivePack",
			Handler:       _SmartHTTPService_InfoRefsReceivePack_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "PostUploadPack",
			Handler:       _SmartHTTPService_PostUploadPack_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "PostReceivePack",
			Handler:       _SmartHTTPService_PostReceivePack_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "smarthttp.proto",
}
