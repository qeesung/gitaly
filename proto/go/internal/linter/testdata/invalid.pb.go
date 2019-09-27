// Code generated by protoc-gen-go. DO NOT EDIT.
// source: go/internal/linter/testdata/invalid.proto

package test

import proto "github.com/golang/protobuf/proto"
import fmt "fmt"
import math "math"
import _ "gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion2 // please upgrade the proto package

type InvalidMethodRequest struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *InvalidMethodRequest) Reset()         { *m = InvalidMethodRequest{} }
func (m *InvalidMethodRequest) String() string { return proto.CompactTextString(m) }
func (*InvalidMethodRequest) ProtoMessage()    {}
func (*InvalidMethodRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_invalid_1fd1290907d704eb, []int{0}
}
func (m *InvalidMethodRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_InvalidMethodRequest.Unmarshal(m, b)
}
func (m *InvalidMethodRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_InvalidMethodRequest.Marshal(b, m, deterministic)
}
func (dst *InvalidMethodRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_InvalidMethodRequest.Merge(dst, src)
}
func (m *InvalidMethodRequest) XXX_Size() int {
	return xxx_messageInfo_InvalidMethodRequest.Size(m)
}
func (m *InvalidMethodRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_InvalidMethodRequest.DiscardUnknown(m)
}

var xxx_messageInfo_InvalidMethodRequest proto.InternalMessageInfo

type InvalidTargetType struct {
	WrongType            int32    `protobuf:"varint,1,opt,name=wrong_type,json=wrongType,proto3" json:"wrong_type,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *InvalidTargetType) Reset()         { *m = InvalidTargetType{} }
func (m *InvalidTargetType) String() string { return proto.CompactTextString(m) }
func (*InvalidTargetType) ProtoMessage()    {}
func (*InvalidTargetType) Descriptor() ([]byte, []int) {
	return fileDescriptor_invalid_1fd1290907d704eb, []int{1}
}
func (m *InvalidTargetType) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_InvalidTargetType.Unmarshal(m, b)
}
func (m *InvalidTargetType) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_InvalidTargetType.Marshal(b, m, deterministic)
}
func (dst *InvalidTargetType) XXX_Merge(src proto.Message) {
	xxx_messageInfo_InvalidTargetType.Merge(dst, src)
}
func (m *InvalidTargetType) XXX_Size() int {
	return xxx_messageInfo_InvalidTargetType.Size(m)
}
func (m *InvalidTargetType) XXX_DiscardUnknown() {
	xxx_messageInfo_InvalidTargetType.DiscardUnknown(m)
}

var xxx_messageInfo_InvalidTargetType proto.InternalMessageInfo

func (m *InvalidTargetType) GetWrongType() int32 {
	if m != nil {
		return m.WrongType
	}
	return 0
}

type InvalidMethodResponse struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *InvalidMethodResponse) Reset()         { *m = InvalidMethodResponse{} }
func (m *InvalidMethodResponse) String() string { return proto.CompactTextString(m) }
func (*InvalidMethodResponse) ProtoMessage()    {}
func (*InvalidMethodResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_invalid_1fd1290907d704eb, []int{2}
}
func (m *InvalidMethodResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_InvalidMethodResponse.Unmarshal(m, b)
}
func (m *InvalidMethodResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_InvalidMethodResponse.Marshal(b, m, deterministic)
}
func (dst *InvalidMethodResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_InvalidMethodResponse.Merge(dst, src)
}
func (m *InvalidMethodResponse) XXX_Size() int {
	return xxx_messageInfo_InvalidMethodResponse.Size(m)
}
func (m *InvalidMethodResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_InvalidMethodResponse.DiscardUnknown(m)
}

var xxx_messageInfo_InvalidMethodResponse proto.InternalMessageInfo

type InvalidNestedRequest struct {
	InnerMessage         *InvalidTargetType `protobuf:"bytes,1,opt,name=inner_message,json=innerMessage,proto3" json:"inner_message,omitempty"`
	XXX_NoUnkeyedLiteral struct{}           `json:"-"`
	XXX_unrecognized     []byte             `json:"-"`
	XXX_sizecache        int32              `json:"-"`
}

func (m *InvalidNestedRequest) Reset()         { *m = InvalidNestedRequest{} }
func (m *InvalidNestedRequest) String() string { return proto.CompactTextString(m) }
func (*InvalidNestedRequest) ProtoMessage()    {}
func (*InvalidNestedRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_invalid_1fd1290907d704eb, []int{3}
}
func (m *InvalidNestedRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_InvalidNestedRequest.Unmarshal(m, b)
}
func (m *InvalidNestedRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_InvalidNestedRequest.Marshal(b, m, deterministic)
}
func (dst *InvalidNestedRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_InvalidNestedRequest.Merge(dst, src)
}
func (m *InvalidNestedRequest) XXX_Size() int {
	return xxx_messageInfo_InvalidNestedRequest.Size(m)
}
func (m *InvalidNestedRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_InvalidNestedRequest.DiscardUnknown(m)
}

var xxx_messageInfo_InvalidNestedRequest proto.InternalMessageInfo

func (m *InvalidNestedRequest) GetInnerMessage() *InvalidTargetType {
	if m != nil {
		return m.InnerMessage
	}
	return nil
}

func init() {
	proto.RegisterType((*InvalidMethodRequest)(nil), "test.InvalidMethodRequest")
	proto.RegisterType((*InvalidTargetType)(nil), "test.InvalidTargetType")
	proto.RegisterType((*InvalidMethodResponse)(nil), "test.InvalidMethodResponse")
	proto.RegisterType((*InvalidNestedRequest)(nil), "test.InvalidNestedRequest")
}

func init() {
	proto.RegisterFile("go/internal/linter/testdata/invalid.proto", fileDescriptor_invalid_1fd1290907d704eb)
}

var fileDescriptor_invalid_1fd1290907d704eb = []byte{
	// 379 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xac, 0xd4, 0xcd, 0x4e, 0x83, 0x40,
	0x10, 0x07, 0x70, 0xb7, 0xb6, 0x95, 0x6e, 0x5b, 0xad, 0x1b, 0xb5, 0x06, 0x63, 0x62, 0x38, 0xe1,
	0x05, 0x0a, 0xf5, 0x33, 0xf1, 0x05, 0x8c, 0xa9, 0x89, 0x2d, 0xb1, 0xf1, 0xd4, 0xa0, 0x9d, 0x50,
	0x92, 0x0a, 0xc8, 0xae, 0x35, 0x7d, 0x0b, 0x3d, 0x71, 0xf0, 0xe0, 0x2b, 0x7a, 0xe6, 0x64, 0x0a,
	0xd8, 0x84, 0xa5, 0x26, 0x0d, 0xf5, 0x46, 0x66, 0x27, 0xbf, 0xfd, 0x2f, 0xcc, 0x82, 0x8f, 0x2d,
	0x57, 0xb5, 0x1d, 0x06, 0xbe, 0x63, 0x8e, 0xd5, 0x71, 0xf4, 0xa4, 0x32, 0xa0, 0x6c, 0x68, 0x32,
	0x53, 0xb5, 0x9d, 0x89, 0x39, 0xb6, 0x87, 0x8a, 0xe7, 0xbb, 0xcc, 0x25, 0xc5, 0x59, 0x5d, 0xac,
	0xd1, 0x91, 0xe9, 0x43, 0x52, 0x93, 0xf6, 0xf0, 0xce, 0x75, 0xdc, 0xd4, 0x01, 0x36, 0x72, 0x87,
	0x5d, 0x78, 0x79, 0x05, 0xca, 0x24, 0x1d, 0x6f, 0x27, 0x75, 0xc3, 0xf4, 0x2d, 0x60, 0xc6, 0xd4,
	0x03, 0x72, 0x88, 0xf1, 0x9b, 0xef, 0x3a, 0xd6, 0x80, 0x4d, 0x3d, 0xd8, 0x47, 0x47, 0x48, 0x2e,
	0x75, 0x2b, 0x51, 0x65, 0xb6, 0x2c, 0x35, 0xf1, 0x2e, 0x67, 0x51, 0xcf, 0x75, 0x28, 0x48, 0xc6,
	0x7c, 0x93, 0x5b, 0xa0, 0x0c, 0x7e, 0x37, 0x21, 0x57, 0xb8, 0x6e, 0x3b, 0x0e, 0xf8, 0x83, 0x67,
	0xa0, 0xd4, 0xb4, 0x62, 0xb2, 0xaa, 0x37, 0x95, 0x59, 0x50, 0x25, 0xb3, 0x7f, 0xb7, 0x16, 0x75,
	0x77, 0xe2, 0x66, 0xfd, 0x43, 0xc0, 0x9b, 0x49, 0x4f, 0x0f, 0xfc, 0x89, 0xfd, 0x04, 0xe4, 0x66,
	0x5e, 0x89, 0x13, 0xb4, 0x88, 0x98, 0xb2, 0x52, 0x67, 0x14, 0x0f, 0x16, 0xae, 0x25, 0x99, 0xd7,
	0xc8, 0x1d, 0x87, 0x69, 0xf9, 0xb1, 0x72, 0x18, 0xc8, 0x05, 0x21, 0x4b, 0xea, 0xab, 0x92, 0x05,
	0x72, 0xcf, 0x91, 0xed, 0xfc, 0x64, 0x35, 0x0c, 0xe4, 0x0d, 0x01, 0x35, 0x90, 0x88, 0xb4, 0x4c,
	0xd4, 0x93, 0x55, 0xa3, 0x22, 0xd2, 0xe7, 0xc8, 0xd3, 0xfc, 0x64, 0x2d, 0x0c, 0x64, 0x41, 0x40,
	0x62, 0xf1, 0xfb, 0xeb, 0xfd, 0x93, 0x18, 0x1c, 0x7c, 0x96, 0x1f, 0xae, 0x84, 0x81, 0x5c, 0x12,
	0x16, 0xbe, 0x81, 0x73, 0xf2, 0xd7, 0x60, 0x2e, 0x4d, 0xf6, 0x38, 0xf2, 0x22, 0x27, 0x99, 0x7c,
	0x29, 0x71, 0x5d, 0x53, 0xb4, 0xcc, 0x04, 0x5c, 0x72, 0xa7, 0x4f, 0xdd, 0xb9, 0xa5, 0x5d, 0x9d,
	0xf4, 0xf1, 0x56, 0x7a, 0xfe, 0x5b, 0xff, 0x02, 0x6b, 0xe4, 0x81, 0x87, 0x57, 0xb8, 0x59, 0xf5,
	0x30, 0x90, 0x2b, 0x02, 0x6a, 0x14, 0x22, 0xfa, 0xb1, 0x1c, 0xfd, 0xd5, 0xda, 0x3f, 0x01, 0x00,
	0x00, 0xff, 0xff, 0x1d, 0xd6, 0x03, 0xa0, 0x16, 0x05, 0x00, 0x00,
}
