// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v4.23.1
// source: namespace.proto

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

// NamespaceServiceClient is the client API for NamespaceService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type NamespaceServiceClient interface {
	// AddNamespace ...
	AddNamespace(ctx context.Context, in *AddNamespaceRequest, opts ...grpc.CallOption) (*AddNamespaceResponse, error)
	// RemoveNamespace ...
	RemoveNamespace(ctx context.Context, in *RemoveNamespaceRequest, opts ...grpc.CallOption) (*RemoveNamespaceResponse, error)
	// RenameNamespace ...
	RenameNamespace(ctx context.Context, in *RenameNamespaceRequest, opts ...grpc.CallOption) (*RenameNamespaceResponse, error)
	// NamespaceExists ...
	NamespaceExists(ctx context.Context, in *NamespaceExistsRequest, opts ...grpc.CallOption) (*NamespaceExistsResponse, error)
}

type namespaceServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewNamespaceServiceClient(cc grpc.ClientConnInterface) NamespaceServiceClient {
	return &namespaceServiceClient{cc}
}

func (c *namespaceServiceClient) AddNamespace(ctx context.Context, in *AddNamespaceRequest, opts ...grpc.CallOption) (*AddNamespaceResponse, error) {
	out := new(AddNamespaceResponse)
	err := c.cc.Invoke(ctx, "/gitaly.NamespaceService/AddNamespace", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *namespaceServiceClient) RemoveNamespace(ctx context.Context, in *RemoveNamespaceRequest, opts ...grpc.CallOption) (*RemoveNamespaceResponse, error) {
	out := new(RemoveNamespaceResponse)
	err := c.cc.Invoke(ctx, "/gitaly.NamespaceService/RemoveNamespace", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *namespaceServiceClient) RenameNamespace(ctx context.Context, in *RenameNamespaceRequest, opts ...grpc.CallOption) (*RenameNamespaceResponse, error) {
	out := new(RenameNamespaceResponse)
	err := c.cc.Invoke(ctx, "/gitaly.NamespaceService/RenameNamespace", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *namespaceServiceClient) NamespaceExists(ctx context.Context, in *NamespaceExistsRequest, opts ...grpc.CallOption) (*NamespaceExistsResponse, error) {
	out := new(NamespaceExistsResponse)
	err := c.cc.Invoke(ctx, "/gitaly.NamespaceService/NamespaceExists", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// NamespaceServiceServer is the server API for NamespaceService service.
// All implementations must embed UnimplementedNamespaceServiceServer
// for forward compatibility
type NamespaceServiceServer interface {
	// AddNamespace ...
	AddNamespace(context.Context, *AddNamespaceRequest) (*AddNamespaceResponse, error)
	// RemoveNamespace ...
	RemoveNamespace(context.Context, *RemoveNamespaceRequest) (*RemoveNamespaceResponse, error)
	// RenameNamespace ...
	RenameNamespace(context.Context, *RenameNamespaceRequest) (*RenameNamespaceResponse, error)
	// NamespaceExists ...
	NamespaceExists(context.Context, *NamespaceExistsRequest) (*NamespaceExistsResponse, error)
	mustEmbedUnimplementedNamespaceServiceServer()
}

// UnimplementedNamespaceServiceServer must be embedded to have forward compatible implementations.
type UnimplementedNamespaceServiceServer struct {
}

func (UnimplementedNamespaceServiceServer) AddNamespace(context.Context, *AddNamespaceRequest) (*AddNamespaceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AddNamespace not implemented")
}
func (UnimplementedNamespaceServiceServer) RemoveNamespace(context.Context, *RemoveNamespaceRequest) (*RemoveNamespaceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RemoveNamespace not implemented")
}
func (UnimplementedNamespaceServiceServer) RenameNamespace(context.Context, *RenameNamespaceRequest) (*RenameNamespaceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RenameNamespace not implemented")
}
func (UnimplementedNamespaceServiceServer) NamespaceExists(context.Context, *NamespaceExistsRequest) (*NamespaceExistsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NamespaceExists not implemented")
}
func (UnimplementedNamespaceServiceServer) mustEmbedUnimplementedNamespaceServiceServer() {}

// UnsafeNamespaceServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to NamespaceServiceServer will
// result in compilation errors.
type UnsafeNamespaceServiceServer interface {
	mustEmbedUnimplementedNamespaceServiceServer()
}

func RegisterNamespaceServiceServer(s grpc.ServiceRegistrar, srv NamespaceServiceServer) {
	s.RegisterService(&NamespaceService_ServiceDesc, srv)
}

func _NamespaceService_AddNamespace_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(AddNamespaceRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(NamespaceServiceServer).AddNamespace(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.NamespaceService/AddNamespace",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(NamespaceServiceServer).AddNamespace(ctx, req.(*AddNamespaceRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _NamespaceService_RemoveNamespace_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RemoveNamespaceRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(NamespaceServiceServer).RemoveNamespace(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.NamespaceService/RemoveNamespace",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(NamespaceServiceServer).RemoveNamespace(ctx, req.(*RemoveNamespaceRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _NamespaceService_RenameNamespace_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RenameNamespaceRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(NamespaceServiceServer).RenameNamespace(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.NamespaceService/RenameNamespace",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(NamespaceServiceServer).RenameNamespace(ctx, req.(*RenameNamespaceRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _NamespaceService_NamespaceExists_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(NamespaceExistsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(NamespaceServiceServer).NamespaceExists(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.NamespaceService/NamespaceExists",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(NamespaceServiceServer).NamespaceExists(ctx, req.(*NamespaceExistsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// NamespaceService_ServiceDesc is the grpc.ServiceDesc for NamespaceService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var NamespaceService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.NamespaceService",
	HandlerType: (*NamespaceServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "AddNamespace",
			Handler:    _NamespaceService_AddNamespace_Handler,
		},
		{
			MethodName: "RemoveNamespace",
			Handler:    _NamespaceService_RemoveNamespace_Handler,
		},
		{
			MethodName: "RenameNamespace",
			Handler:    _NamespaceService_RenameNamespace_Handler,
		},
		{
			MethodName: "NamespaceExists",
			Handler:    _NamespaceService_NamespaceExists_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "namespace.proto",
}
