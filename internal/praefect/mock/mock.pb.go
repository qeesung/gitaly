// Code generated by protoc-gen-go. DO NOT EDIT.
// source: praefect/mock/mock.proto

package mock

import (
	context "context"
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
	empty "github.com/golang/protobuf/ptypes/empty"
	gitalypb "gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion3 // please upgrade the proto package

type RepoRequest struct {
	Repo                 *gitalypb.Repository `protobuf:"bytes,1,opt,name=repo,proto3" json:"repo,omitempty"`
	XXX_NoUnkeyedLiteral struct{}             `json:"-"`
	XXX_unrecognized     []byte               `json:"-"`
	XXX_sizecache        int32                `json:"-"`
}

func (m *RepoRequest) Reset()         { *m = RepoRequest{} }
func (m *RepoRequest) String() string { return proto.CompactTextString(m) }
func (*RepoRequest) ProtoMessage()    {}
func (*RepoRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_d20d83172fd49eb0, []int{0}
}

func (m *RepoRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_RepoRequest.Unmarshal(m, b)
}
func (m *RepoRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_RepoRequest.Marshal(b, m, deterministic)
}
func (m *RepoRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_RepoRequest.Merge(m, src)
}
func (m *RepoRequest) XXX_Size() int {
	return xxx_messageInfo_RepoRequest.Size(m)
}
func (m *RepoRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_RepoRequest.DiscardUnknown(m)
}

var xxx_messageInfo_RepoRequest proto.InternalMessageInfo

func (m *RepoRequest) GetRepo() *gitalypb.Repository {
	if m != nil {
		return m.Repo
	}
	return nil
}

func init() {
	proto.RegisterType((*RepoRequest)(nil), "mock.RepoRequest")
}

func init() { proto.RegisterFile("praefect/mock/mock.proto", fileDescriptor_d20d83172fd49eb0) }

var fileDescriptor_d20d83172fd49eb0 = []byte{
	// 230 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xe2, 0x92, 0x28, 0x28, 0x4a, 0x4c,
	0x4d, 0x4b, 0x4d, 0x2e, 0xd1, 0xcf, 0xcd, 0x4f, 0xce, 0x06, 0x13, 0x7a, 0x05, 0x45, 0xf9, 0x25,
	0xf9, 0x42, 0x2c, 0x20, 0xb6, 0x14, 0x4f, 0x71, 0x46, 0x62, 0x51, 0x6a, 0x0a, 0x44, 0x4c, 0x8a,
	0x2b, 0x27, 0x33, 0xaf, 0x04, 0xca, 0x96, 0x4e, 0xcf, 0xcf, 0x4f, 0xcf, 0x49, 0xd5, 0x07, 0xf3,
	0x92, 0x4a, 0xd3, 0xf4, 0x53, 0x73, 0x0b, 0x4a, 0x2a, 0x21, 0x92, 0x4a, 0xd6, 0x5c, 0xdc, 0x41,
	0xa9, 0x05, 0xf9, 0x41, 0xa9, 0x85, 0xa5, 0xa9, 0xc5, 0x25, 0x42, 0x3a, 0x5c, 0x2c, 0x45, 0xa9,
	0x05, 0xf9, 0x12, 0x8c, 0x0a, 0x8c, 0x1a, 0xdc, 0x46, 0x42, 0x7a, 0xe9, 0x99, 0x25, 0x89, 0x39,
	0x95, 0x7a, 0x20, 0x25, 0xc5, 0x99, 0x25, 0xf9, 0x45, 0x95, 0x4e, 0x2c, 0x33, 0x8e, 0xe9, 0x30,
	0x06, 0x81, 0x55, 0x19, 0xcd, 0x63, 0xe4, 0xe2, 0x0d, 0xce, 0xcc, 0x2d, 0xc8, 0x49, 0x0d, 0x4e,
	0x2d, 0x2a, 0xcb, 0x4c, 0x4e, 0x15, 0x72, 0xe3, 0x12, 0x04, 0xa9, 0x75, 0x4c, 0x4e, 0x4e, 0x2d,
	0x2e, 0xce, 0x2f, 0x0a, 0xcd, 0x4b, 0x2c, 0xaa, 0x14, 0x12, 0xd4, 0x03, 0xbb, 0x16, 0xc9, 0x1e,
	0x29, 0x31, 0x3d, 0x88, 0xa3, 0xf4, 0x60, 0x8e, 0xd2, 0x73, 0x05, 0x39, 0x4a, 0x89, 0xed, 0xd7,
	0x74, 0x0d, 0x26, 0x0e, 0x26, 0x21, 0x57, 0x2e, 0x01, 0x90, 0x72, 0xdf, 0xd2, 0x92, 0xc4, 0x12,
	0xb2, 0x8d, 0x61, 0x4c, 0x62, 0x03, 0x8b, 0x1b, 0x03, 0x02, 0x00, 0x00, 0xff, 0xff, 0x46, 0xa4,
	0xc0, 0xce, 0x3d, 0x01, 0x00, 0x00,
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// SimpleServiceClient is the client API for SimpleService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type SimpleServiceClient interface {
	// RepoAccessorUnary is a unary RPC that accesses a repo
	RepoAccessorUnary(ctx context.Context, in *RepoRequest, opts ...grpc.CallOption) (*empty.Empty, error)
	// RepoMutatorUnary is a unary RPC that mutates a repo
	RepoMutatorUnary(ctx context.Context, in *RepoRequest, opts ...grpc.CallOption) (*empty.Empty, error)
}

type simpleServiceClient struct {
	cc *grpc.ClientConn
}

func NewSimpleServiceClient(cc *grpc.ClientConn) SimpleServiceClient {
	return &simpleServiceClient{cc}
}

func (c *simpleServiceClient) RepoAccessorUnary(ctx context.Context, in *RepoRequest, opts ...grpc.CallOption) (*empty.Empty, error) {
	out := new(empty.Empty)
	err := c.cc.Invoke(ctx, "/mock.SimpleService/RepoAccessorUnary", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *simpleServiceClient) RepoMutatorUnary(ctx context.Context, in *RepoRequest, opts ...grpc.CallOption) (*empty.Empty, error) {
	out := new(empty.Empty)
	err := c.cc.Invoke(ctx, "/mock.SimpleService/RepoMutatorUnary", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SimpleServiceServer is the server API for SimpleService service.
type SimpleServiceServer interface {
	// RepoAccessorUnary is a unary RPC that accesses a repo
	RepoAccessorUnary(context.Context, *RepoRequest) (*empty.Empty, error)
	// RepoMutatorUnary is a unary RPC that mutates a repo
	RepoMutatorUnary(context.Context, *RepoRequest) (*empty.Empty, error)
}

// UnimplementedSimpleServiceServer can be embedded to have forward compatible implementations.
type UnimplementedSimpleServiceServer struct {
}

func (*UnimplementedSimpleServiceServer) RepoAccessorUnary(ctx context.Context, req *RepoRequest) (*empty.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RepoAccessorUnary not implemented")
}
func (*UnimplementedSimpleServiceServer) RepoMutatorUnary(ctx context.Context, req *RepoRequest) (*empty.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RepoMutatorUnary not implemented")
}

func RegisterSimpleServiceServer(s *grpc.Server, srv SimpleServiceServer) {
	s.RegisterService(&_SimpleService_serviceDesc, srv)
}

func _SimpleService_RepoAccessorUnary_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RepoRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SimpleServiceServer).RepoAccessorUnary(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/mock.SimpleService/RepoAccessorUnary",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SimpleServiceServer).RepoAccessorUnary(ctx, req.(*RepoRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _SimpleService_RepoMutatorUnary_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RepoRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SimpleServiceServer).RepoMutatorUnary(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/mock.SimpleService/RepoMutatorUnary",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SimpleServiceServer).RepoMutatorUnary(ctx, req.(*RepoRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _SimpleService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "mock.SimpleService",
	HandlerType: (*SimpleServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "RepoAccessorUnary",
			Handler:    _SimpleService_RepoAccessorUnary_Handler,
		},
		{
			MethodName: "RepoMutatorUnary",
			Handler:    _SimpleService_RepoMutatorUnary_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "praefect/mock/mock.proto",
}
