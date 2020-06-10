// Code generated by protoc-gen-go. DO NOT EDIT.
// source: hook.proto

package gitalypb

import (
	context "context"
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
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

type PreReceiveHookRequest struct {
	Repository           *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	EnvironmentVariables []string    `protobuf:"bytes,2,rep,name=environment_variables,json=environmentVariables,proto3" json:"environment_variables,omitempty"`
	Stdin                []byte      `protobuf:"bytes,4,opt,name=stdin,proto3" json:"stdin,omitempty"`
	GitPushOptions       []string    `protobuf:"bytes,5,rep,name=git_push_options,json=gitPushOptions,proto3" json:"git_push_options,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *PreReceiveHookRequest) Reset()         { *m = PreReceiveHookRequest{} }
func (m *PreReceiveHookRequest) String() string { return proto.CompactTextString(m) }
func (*PreReceiveHookRequest) ProtoMessage()    {}
func (*PreReceiveHookRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_3eef30da1c11ee1b, []int{0}
}

func (m *PreReceiveHookRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_PreReceiveHookRequest.Unmarshal(m, b)
}
func (m *PreReceiveHookRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_PreReceiveHookRequest.Marshal(b, m, deterministic)
}
func (m *PreReceiveHookRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PreReceiveHookRequest.Merge(m, src)
}
func (m *PreReceiveHookRequest) XXX_Size() int {
	return xxx_messageInfo_PreReceiveHookRequest.Size(m)
}
func (m *PreReceiveHookRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_PreReceiveHookRequest.DiscardUnknown(m)
}

var xxx_messageInfo_PreReceiveHookRequest proto.InternalMessageInfo

func (m *PreReceiveHookRequest) GetRepository() *Repository {
	if m != nil {
		return m.Repository
	}
	return nil
}

func (m *PreReceiveHookRequest) GetEnvironmentVariables() []string {
	if m != nil {
		return m.EnvironmentVariables
	}
	return nil
}

func (m *PreReceiveHookRequest) GetStdin() []byte {
	if m != nil {
		return m.Stdin
	}
	return nil
}

func (m *PreReceiveHookRequest) GetGitPushOptions() []string {
	if m != nil {
		return m.GitPushOptions
	}
	return nil
}

type PreReceiveHookResponse struct {
	Stdout               []byte      `protobuf:"bytes,1,opt,name=stdout,proto3" json:"stdout,omitempty"`
	Stderr               []byte      `protobuf:"bytes,2,opt,name=stderr,proto3" json:"stderr,omitempty"`
	ExitStatus           *ExitStatus `protobuf:"bytes,3,opt,name=exit_status,json=exitStatus,proto3" json:"exit_status,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *PreReceiveHookResponse) Reset()         { *m = PreReceiveHookResponse{} }
func (m *PreReceiveHookResponse) String() string { return proto.CompactTextString(m) }
func (*PreReceiveHookResponse) ProtoMessage()    {}
func (*PreReceiveHookResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_3eef30da1c11ee1b, []int{1}
}

func (m *PreReceiveHookResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_PreReceiveHookResponse.Unmarshal(m, b)
}
func (m *PreReceiveHookResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_PreReceiveHookResponse.Marshal(b, m, deterministic)
}
func (m *PreReceiveHookResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PreReceiveHookResponse.Merge(m, src)
}
func (m *PreReceiveHookResponse) XXX_Size() int {
	return xxx_messageInfo_PreReceiveHookResponse.Size(m)
}
func (m *PreReceiveHookResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_PreReceiveHookResponse.DiscardUnknown(m)
}

var xxx_messageInfo_PreReceiveHookResponse proto.InternalMessageInfo

func (m *PreReceiveHookResponse) GetStdout() []byte {
	if m != nil {
		return m.Stdout
	}
	return nil
}

func (m *PreReceiveHookResponse) GetStderr() []byte {
	if m != nil {
		return m.Stderr
	}
	return nil
}

func (m *PreReceiveHookResponse) GetExitStatus() *ExitStatus {
	if m != nil {
		return m.ExitStatus
	}
	return nil
}

type PostReceiveHookRequest struct {
	Repository           *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	EnvironmentVariables []string    `protobuf:"bytes,2,rep,name=environment_variables,json=environmentVariables,proto3" json:"environment_variables,omitempty"`
	Stdin                []byte      `protobuf:"bytes,3,opt,name=stdin,proto3" json:"stdin,omitempty"`
	GitPushOptions       []string    `protobuf:"bytes,4,rep,name=git_push_options,json=gitPushOptions,proto3" json:"git_push_options,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *PostReceiveHookRequest) Reset()         { *m = PostReceiveHookRequest{} }
func (m *PostReceiveHookRequest) String() string { return proto.CompactTextString(m) }
func (*PostReceiveHookRequest) ProtoMessage()    {}
func (*PostReceiveHookRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_3eef30da1c11ee1b, []int{2}
}

func (m *PostReceiveHookRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_PostReceiveHookRequest.Unmarshal(m, b)
}
func (m *PostReceiveHookRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_PostReceiveHookRequest.Marshal(b, m, deterministic)
}
func (m *PostReceiveHookRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PostReceiveHookRequest.Merge(m, src)
}
func (m *PostReceiveHookRequest) XXX_Size() int {
	return xxx_messageInfo_PostReceiveHookRequest.Size(m)
}
func (m *PostReceiveHookRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_PostReceiveHookRequest.DiscardUnknown(m)
}

var xxx_messageInfo_PostReceiveHookRequest proto.InternalMessageInfo

func (m *PostReceiveHookRequest) GetRepository() *Repository {
	if m != nil {
		return m.Repository
	}
	return nil
}

func (m *PostReceiveHookRequest) GetEnvironmentVariables() []string {
	if m != nil {
		return m.EnvironmentVariables
	}
	return nil
}

func (m *PostReceiveHookRequest) GetStdin() []byte {
	if m != nil {
		return m.Stdin
	}
	return nil
}

func (m *PostReceiveHookRequest) GetGitPushOptions() []string {
	if m != nil {
		return m.GitPushOptions
	}
	return nil
}

type PostReceiveHookResponse struct {
	Stdout               []byte      `protobuf:"bytes,1,opt,name=stdout,proto3" json:"stdout,omitempty"`
	Stderr               []byte      `protobuf:"bytes,2,opt,name=stderr,proto3" json:"stderr,omitempty"`
	ExitStatus           *ExitStatus `protobuf:"bytes,3,opt,name=exit_status,json=exitStatus,proto3" json:"exit_status,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *PostReceiveHookResponse) Reset()         { *m = PostReceiveHookResponse{} }
func (m *PostReceiveHookResponse) String() string { return proto.CompactTextString(m) }
func (*PostReceiveHookResponse) ProtoMessage()    {}
func (*PostReceiveHookResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_3eef30da1c11ee1b, []int{3}
}

func (m *PostReceiveHookResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_PostReceiveHookResponse.Unmarshal(m, b)
}
func (m *PostReceiveHookResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_PostReceiveHookResponse.Marshal(b, m, deterministic)
}
func (m *PostReceiveHookResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_PostReceiveHookResponse.Merge(m, src)
}
func (m *PostReceiveHookResponse) XXX_Size() int {
	return xxx_messageInfo_PostReceiveHookResponse.Size(m)
}
func (m *PostReceiveHookResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_PostReceiveHookResponse.DiscardUnknown(m)
}

var xxx_messageInfo_PostReceiveHookResponse proto.InternalMessageInfo

func (m *PostReceiveHookResponse) GetStdout() []byte {
	if m != nil {
		return m.Stdout
	}
	return nil
}

func (m *PostReceiveHookResponse) GetStderr() []byte {
	if m != nil {
		return m.Stderr
	}
	return nil
}

func (m *PostReceiveHookResponse) GetExitStatus() *ExitStatus {
	if m != nil {
		return m.ExitStatus
	}
	return nil
}

type UpdateHookRequest struct {
	Repository           *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	EnvironmentVariables []string    `protobuf:"bytes,2,rep,name=environment_variables,json=environmentVariables,proto3" json:"environment_variables,omitempty"`
	Ref                  []byte      `protobuf:"bytes,3,opt,name=ref,proto3" json:"ref,omitempty"`
	OldValue             string      `protobuf:"bytes,4,opt,name=old_value,json=oldValue,proto3" json:"old_value,omitempty"`
	NewValue             string      `protobuf:"bytes,5,opt,name=new_value,json=newValue,proto3" json:"new_value,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *UpdateHookRequest) Reset()         { *m = UpdateHookRequest{} }
func (m *UpdateHookRequest) String() string { return proto.CompactTextString(m) }
func (*UpdateHookRequest) ProtoMessage()    {}
func (*UpdateHookRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_3eef30da1c11ee1b, []int{4}
}

func (m *UpdateHookRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_UpdateHookRequest.Unmarshal(m, b)
}
func (m *UpdateHookRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_UpdateHookRequest.Marshal(b, m, deterministic)
}
func (m *UpdateHookRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_UpdateHookRequest.Merge(m, src)
}
func (m *UpdateHookRequest) XXX_Size() int {
	return xxx_messageInfo_UpdateHookRequest.Size(m)
}
func (m *UpdateHookRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_UpdateHookRequest.DiscardUnknown(m)
}

var xxx_messageInfo_UpdateHookRequest proto.InternalMessageInfo

func (m *UpdateHookRequest) GetRepository() *Repository {
	if m != nil {
		return m.Repository
	}
	return nil
}

func (m *UpdateHookRequest) GetEnvironmentVariables() []string {
	if m != nil {
		return m.EnvironmentVariables
	}
	return nil
}

func (m *UpdateHookRequest) GetRef() []byte {
	if m != nil {
		return m.Ref
	}
	return nil
}

func (m *UpdateHookRequest) GetOldValue() string {
	if m != nil {
		return m.OldValue
	}
	return ""
}

func (m *UpdateHookRequest) GetNewValue() string {
	if m != nil {
		return m.NewValue
	}
	return ""
}

type UpdateHookResponse struct {
	Stdout               []byte      `protobuf:"bytes,1,opt,name=stdout,proto3" json:"stdout,omitempty"`
	Stderr               []byte      `protobuf:"bytes,2,opt,name=stderr,proto3" json:"stderr,omitempty"`
	ExitStatus           *ExitStatus `protobuf:"bytes,3,opt,name=exit_status,json=exitStatus,proto3" json:"exit_status,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *UpdateHookResponse) Reset()         { *m = UpdateHookResponse{} }
func (m *UpdateHookResponse) String() string { return proto.CompactTextString(m) }
func (*UpdateHookResponse) ProtoMessage()    {}
func (*UpdateHookResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_3eef30da1c11ee1b, []int{5}
}

func (m *UpdateHookResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_UpdateHookResponse.Unmarshal(m, b)
}
func (m *UpdateHookResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_UpdateHookResponse.Marshal(b, m, deterministic)
}
func (m *UpdateHookResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_UpdateHookResponse.Merge(m, src)
}
func (m *UpdateHookResponse) XXX_Size() int {
	return xxx_messageInfo_UpdateHookResponse.Size(m)
}
func (m *UpdateHookResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_UpdateHookResponse.DiscardUnknown(m)
}

var xxx_messageInfo_UpdateHookResponse proto.InternalMessageInfo

func (m *UpdateHookResponse) GetStdout() []byte {
	if m != nil {
		return m.Stdout
	}
	return nil
}

func (m *UpdateHookResponse) GetStderr() []byte {
	if m != nil {
		return m.Stderr
	}
	return nil
}

func (m *UpdateHookResponse) GetExitStatus() *ExitStatus {
	if m != nil {
		return m.ExitStatus
	}
	return nil
}

func init() {
	proto.RegisterType((*PreReceiveHookRequest)(nil), "gitaly.PreReceiveHookRequest")
	proto.RegisterType((*PreReceiveHookResponse)(nil), "gitaly.PreReceiveHookResponse")
	proto.RegisterType((*PostReceiveHookRequest)(nil), "gitaly.PostReceiveHookRequest")
	proto.RegisterType((*PostReceiveHookResponse)(nil), "gitaly.PostReceiveHookResponse")
	proto.RegisterType((*UpdateHookRequest)(nil), "gitaly.UpdateHookRequest")
	proto.RegisterType((*UpdateHookResponse)(nil), "gitaly.UpdateHookResponse")
}

func init() { proto.RegisterFile("hook.proto", fileDescriptor_3eef30da1c11ee1b) }

var fileDescriptor_3eef30da1c11ee1b = []byte{
	// 477 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xcc, 0x94, 0xcd, 0x6e, 0xd3, 0x40,
	0x10, 0xc7, 0xb5, 0xce, 0x87, 0x9a, 0x49, 0x54, 0xca, 0xaa, 0x0d, 0xc6, 0x08, 0x88, 0x7c, 0xf2,
	0x01, 0x92, 0xa8, 0xb9, 0x70, 0xae, 0x84, 0xc4, 0x05, 0x11, 0xb9, 0xa2, 0x07, 0x90, 0xb0, 0x9c,
	0x78, 0x70, 0x56, 0x75, 0x3d, 0x66, 0x77, 0xed, 0x36, 0x07, 0x78, 0x0d, 0xe0, 0x85, 0xca, 0x8d,
	0x07, 0xe2, 0x84, 0xfc, 0x91, 0x34, 0x69, 0x5d, 0x71, 0x2b, 0xbd, 0xcd, 0xcc, 0x6f, 0x67, 0x56,
	0xff, 0xbf, 0x66, 0x17, 0x60, 0x41, 0x74, 0x3a, 0x4c, 0x24, 0x69, 0xe2, 0xed, 0x50, 0x68, 0x3f,
	0x5a, 0x5a, 0x10, 0x89, 0x58, 0x97, 0x35, 0xab, 0xa7, 0x16, 0xbe, 0xc4, 0xa0, 0xcc, 0xec, 0x4b,
	0x06, 0x07, 0x53, 0x89, 0x2e, 0xce, 0x51, 0x64, 0xf8, 0x86, 0xe8, 0xd4, 0xc5, 0x2f, 0x29, 0x2a,
	0xcd, 0x5f, 0x01, 0x48, 0x4c, 0x48, 0x09, 0x4d, 0x72, 0x69, 0xb2, 0x01, 0x73, 0xba, 0x87, 0x7c,
	0x58, 0x0e, 0x1c, 0xba, 0x6b, 0x72, 0xd4, 0xfc, 0x71, 0xf9, 0x82, 0xb9, 0x1b, 0x67, 0xf9, 0x04,
	0x0e, 0x30, 0xce, 0x84, 0xa4, 0xf8, 0x0c, 0x63, 0xed, 0x65, 0xbe, 0x14, 0xfe, 0x2c, 0x42, 0x65,
	0x1a, 0x83, 0x86, 0xd3, 0x71, 0xf7, 0x37, 0xe0, 0xc9, 0x8a, 0xf1, 0x7d, 0x68, 0x29, 0x1d, 0x88,
	0xd8, 0x6c, 0x0e, 0x98, 0xd3, 0x73, 0xcb, 0x84, 0x3b, 0xb0, 0x17, 0x0a, 0xed, 0x25, 0xa9, 0x5a,
	0x78, 0x94, 0x68, 0x41, 0xb1, 0x32, 0x5b, 0xc5, 0x94, 0xdd, 0x50, 0xe8, 0x69, 0xaa, 0x16, 0xef,
	0xca, 0xaa, 0xfd, 0x15, 0xfa, 0xd7, 0x75, 0xa8, 0x84, 0x62, 0x85, 0xbc, 0x0f, 0x6d, 0xa5, 0x03,
	0x4a, 0x75, 0x21, 0xa2, 0xe7, 0x56, 0x59, 0x55, 0x47, 0x29, 0x4d, 0x63, 0x5d, 0x47, 0x29, 0xf9,
	0x04, 0xba, 0x78, 0x21, 0xb4, 0xa7, 0xb4, 0xaf, 0x53, 0x65, 0x36, 0xb6, 0x95, 0xbf, 0xbe, 0x10,
	0xfa, 0xb8, 0x20, 0x2e, 0xe0, 0x3a, 0xb6, 0x7f, 0x31, 0xe8, 0x4f, 0x49, 0xe9, 0x7b, 0x64, 0x64,
	0xe3, 0x5f, 0x46, 0x36, 0x6b, 0x8d, 0xfc, 0x06, 0x8f, 0x6e, 0x08, 0xb9, 0x4b, 0x27, 0x7f, 0x33,
	0x78, 0xf8, 0x3e, 0x09, 0x7c, 0xfd, 0x3f, 0x4d, 0xdc, 0x83, 0x86, 0xc4, 0xcf, 0x95, 0x85, 0x79,
	0xc8, 0x9f, 0x40, 0x87, 0xa2, 0xc0, 0xcb, 0xfc, 0x28, 0xc5, 0x62, 0x47, 0x3b, 0xee, 0x0e, 0x45,
	0xc1, 0x49, 0x9e, 0xe7, 0x30, 0xc6, 0xf3, 0x0a, 0xb6, 0x4a, 0x18, 0xe3, 0x79, 0x01, 0xed, 0x25,
	0xf0, 0x4d, 0x3d, 0x77, 0xe8, 0xe5, 0xe1, 0x4f, 0x03, 0xba, 0xf9, 0xad, 0xc7, 0x28, 0x33, 0x31,
	0x47, 0xfe, 0x11, 0x76, 0xb7, 0x1f, 0x09, 0x7f, 0xba, 0x9a, 0x50, 0xfb, 0x09, 0x58, 0xcf, 0x6e,
	0xc3, 0xa5, 0x0a, 0xbb, 0xfd, 0xe7, 0xbb, 0x63, 0xec, 0x18, 0x0e, 0x1b, 0x33, 0xfe, 0x09, 0x1e,
	0x5c, 0x5b, 0x1c, 0x7e, 0xd5, 0x5e, 0xfb, 0x34, 0xac, 0xe7, 0xb7, 0xf2, 0x9a, 0xf9, 0x6f, 0x01,
	0xae, 0x7c, 0xe4, 0x8f, 0x57, 0xad, 0x37, 0x76, 0xc5, 0xb2, 0xea, 0xd0, 0xf6, 0xc0, 0x31, 0x3b,
	0x1a, 0x7f, 0xc8, 0x8f, 0x45, 0xfe, 0x6c, 0x38, 0xa7, 0xb3, 0x51, 0x19, 0xbe, 0x24, 0x19, 0x8e,
	0xca, 0xe6, 0x51, 0xf1, 0x3f, 0x8e, 0x42, 0xaa, 0xf2, 0x64, 0x36, 0x6b, 0x17, 0xa5, 0xc9, 0xdf,
	0x00, 0x00, 0x00, 0xff, 0xff, 0xc2, 0x0d, 0xf2, 0x9e, 0x62, 0x05, 0x00, 0x00,
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// HookServiceClient is the client API for HookService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type HookServiceClient interface {
	PreReceiveHook(ctx context.Context, opts ...grpc.CallOption) (HookService_PreReceiveHookClient, error)
	PostReceiveHook(ctx context.Context, opts ...grpc.CallOption) (HookService_PostReceiveHookClient, error)
	UpdateHook(ctx context.Context, in *UpdateHookRequest, opts ...grpc.CallOption) (HookService_UpdateHookClient, error)
}

type hookServiceClient struct {
	cc *grpc.ClientConn
}

func NewHookServiceClient(cc *grpc.ClientConn) HookServiceClient {
	return &hookServiceClient{cc}
}

func (c *hookServiceClient) PreReceiveHook(ctx context.Context, opts ...grpc.CallOption) (HookService_PreReceiveHookClient, error) {
	stream, err := c.cc.NewStream(ctx, &_HookService_serviceDesc.Streams[0], "/gitaly.HookService/PreReceiveHook", opts...)
	if err != nil {
		return nil, err
	}
	x := &hookServicePreReceiveHookClient{stream}
	return x, nil
}

type HookService_PreReceiveHookClient interface {
	Send(*PreReceiveHookRequest) error
	Recv() (*PreReceiveHookResponse, error)
	grpc.ClientStream
}

type hookServicePreReceiveHookClient struct {
	grpc.ClientStream
}

func (x *hookServicePreReceiveHookClient) Send(m *PreReceiveHookRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *hookServicePreReceiveHookClient) Recv() (*PreReceiveHookResponse, error) {
	m := new(PreReceiveHookResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *hookServiceClient) PostReceiveHook(ctx context.Context, opts ...grpc.CallOption) (HookService_PostReceiveHookClient, error) {
	stream, err := c.cc.NewStream(ctx, &_HookService_serviceDesc.Streams[1], "/gitaly.HookService/PostReceiveHook", opts...)
	if err != nil {
		return nil, err
	}
	x := &hookServicePostReceiveHookClient{stream}
	return x, nil
}

type HookService_PostReceiveHookClient interface {
	Send(*PostReceiveHookRequest) error
	Recv() (*PostReceiveHookResponse, error)
	grpc.ClientStream
}

type hookServicePostReceiveHookClient struct {
	grpc.ClientStream
}

func (x *hookServicePostReceiveHookClient) Send(m *PostReceiveHookRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *hookServicePostReceiveHookClient) Recv() (*PostReceiveHookResponse, error) {
	m := new(PostReceiveHookResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *hookServiceClient) UpdateHook(ctx context.Context, in *UpdateHookRequest, opts ...grpc.CallOption) (HookService_UpdateHookClient, error) {
	stream, err := c.cc.NewStream(ctx, &_HookService_serviceDesc.Streams[2], "/gitaly.HookService/UpdateHook", opts...)
	if err != nil {
		return nil, err
	}
	x := &hookServiceUpdateHookClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type HookService_UpdateHookClient interface {
	Recv() (*UpdateHookResponse, error)
	grpc.ClientStream
}

type hookServiceUpdateHookClient struct {
	grpc.ClientStream
}

func (x *hookServiceUpdateHookClient) Recv() (*UpdateHookResponse, error) {
	m := new(UpdateHookResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// HookServiceServer is the server API for HookService service.
type HookServiceServer interface {
	PreReceiveHook(HookService_PreReceiveHookServer) error
	PostReceiveHook(HookService_PostReceiveHookServer) error
	UpdateHook(*UpdateHookRequest, HookService_UpdateHookServer) error
}

// UnimplementedHookServiceServer can be embedded to have forward compatible implementations.
type UnimplementedHookServiceServer struct {
}

func (*UnimplementedHookServiceServer) PreReceiveHook(srv HookService_PreReceiveHookServer) error {
	return status.Errorf(codes.Unimplemented, "method PreReceiveHook not implemented")
}
func (*UnimplementedHookServiceServer) PostReceiveHook(srv HookService_PostReceiveHookServer) error {
	return status.Errorf(codes.Unimplemented, "method PostReceiveHook not implemented")
}
func (*UnimplementedHookServiceServer) UpdateHook(req *UpdateHookRequest, srv HookService_UpdateHookServer) error {
	return status.Errorf(codes.Unimplemented, "method UpdateHook not implemented")
}

func RegisterHookServiceServer(s *grpc.Server, srv HookServiceServer) {
	s.RegisterService(&_HookService_serviceDesc, srv)
}

func _HookService_PreReceiveHook_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(HookServiceServer).PreReceiveHook(&hookServicePreReceiveHookServer{stream})
}

type HookService_PreReceiveHookServer interface {
	Send(*PreReceiveHookResponse) error
	Recv() (*PreReceiveHookRequest, error)
	grpc.ServerStream
}

type hookServicePreReceiveHookServer struct {
	grpc.ServerStream
}

func (x *hookServicePreReceiveHookServer) Send(m *PreReceiveHookResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *hookServicePreReceiveHookServer) Recv() (*PreReceiveHookRequest, error) {
	m := new(PreReceiveHookRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func _HookService_PostReceiveHook_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(HookServiceServer).PostReceiveHook(&hookServicePostReceiveHookServer{stream})
}

type HookService_PostReceiveHookServer interface {
	Send(*PostReceiveHookResponse) error
	Recv() (*PostReceiveHookRequest, error)
	grpc.ServerStream
}

type hookServicePostReceiveHookServer struct {
	grpc.ServerStream
}

func (x *hookServicePostReceiveHookServer) Send(m *PostReceiveHookResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *hookServicePostReceiveHookServer) Recv() (*PostReceiveHookRequest, error) {
	m := new(PostReceiveHookRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func _HookService_UpdateHook_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(UpdateHookRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(HookServiceServer).UpdateHook(m, &hookServiceUpdateHookServer{stream})
}

type HookService_UpdateHookServer interface {
	Send(*UpdateHookResponse) error
	grpc.ServerStream
}

type hookServiceUpdateHookServer struct {
	grpc.ServerStream
}

func (x *hookServiceUpdateHookServer) Send(m *UpdateHookResponse) error {
	return x.ServerStream.SendMsg(m)
}

var _HookService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.HookService",
	HandlerType: (*HookServiceServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "PreReceiveHook",
			Handler:       _HookService_PreReceiveHook_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "PostReceiveHook",
			Handler:       _HookService_PostReceiveHook_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "UpdateHook",
			Handler:       _HookService_UpdateHook_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "hook.proto",
}
