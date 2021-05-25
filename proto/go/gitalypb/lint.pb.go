// Code generated by protoc-gen-go. DO NOT EDIT.
// source: lint.proto

package gitalypb

import (
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
	descriptor "github.com/golang/protobuf/protoc-gen-go/descriptor"
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

type OperationMsg_Operation int32

const (
	OperationMsg_UNKNOWN  OperationMsg_Operation = 0
	OperationMsg_MUTATOR  OperationMsg_Operation = 1
	OperationMsg_ACCESSOR OperationMsg_Operation = 2
)

var OperationMsg_Operation_name = map[int32]string{
	0: "UNKNOWN",
	1: "MUTATOR",
	2: "ACCESSOR",
}

var OperationMsg_Operation_value = map[string]int32{
	"UNKNOWN":  0,
	"MUTATOR":  1,
	"ACCESSOR": 2,
}

func (x OperationMsg_Operation) String() string {
	return proto.EnumName(OperationMsg_Operation_name, int32(x))
}

func (OperationMsg_Operation) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_1612d42a10b555ca, []int{0, 0}
}

type OperationMsg_Scope int32

const (
	OperationMsg_REPOSITORY OperationMsg_Scope = 0
	OperationMsg_STORAGE    OperationMsg_Scope = 2
)

var OperationMsg_Scope_name = map[int32]string{
	0: "REPOSITORY",
	2: "STORAGE",
}

var OperationMsg_Scope_value = map[string]int32{
	"REPOSITORY": 0,
	"STORAGE":    2,
}

func (x OperationMsg_Scope) String() string {
	return proto.EnumName(OperationMsg_Scope_name, int32(x))
}

func (OperationMsg_Scope) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_1612d42a10b555ca, []int{0, 1}
}

type OperationMsg struct {
	Op OperationMsg_Operation `protobuf:"varint,1,opt,name=op,proto3,enum=gitaly.OperationMsg_Operation" json:"op,omitempty"`
	// Scope level indicates what level an RPC interacts with a server:
	//   - REPOSITORY: scoped to only a single repo
	//   - SERVER: affects the entire server and potentially all repos
	//   - STORAGE: scoped to a specific storage location and all repos within
	ScopeLevel           OperationMsg_Scope `protobuf:"varint,2,opt,name=scope_level,json=scopeLevel,proto3,enum=gitaly.OperationMsg_Scope" json:"scope_level,omitempty"`
	XXX_NoUnkeyedLiteral struct{}           `json:"-"`
	XXX_unrecognized     []byte             `json:"-"`
	XXX_sizecache        int32              `json:"-"`
}

func (m *OperationMsg) Reset()         { *m = OperationMsg{} }
func (m *OperationMsg) String() string { return proto.CompactTextString(m) }
func (*OperationMsg) ProtoMessage()    {}
func (*OperationMsg) Descriptor() ([]byte, []int) {
	return fileDescriptor_1612d42a10b555ca, []int{0}
}

func (m *OperationMsg) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_OperationMsg.Unmarshal(m, b)
}
func (m *OperationMsg) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_OperationMsg.Marshal(b, m, deterministic)
}
func (m *OperationMsg) XXX_Merge(src proto.Message) {
	xxx_messageInfo_OperationMsg.Merge(m, src)
}
func (m *OperationMsg) XXX_Size() int {
	return xxx_messageInfo_OperationMsg.Size(m)
}
func (m *OperationMsg) XXX_DiscardUnknown() {
	xxx_messageInfo_OperationMsg.DiscardUnknown(m)
}

var xxx_messageInfo_OperationMsg proto.InternalMessageInfo

func (m *OperationMsg) GetOp() OperationMsg_Operation {
	if m != nil {
		return m.Op
	}
	return OperationMsg_UNKNOWN
}

func (m *OperationMsg) GetScopeLevel() OperationMsg_Scope {
	if m != nil {
		return m.ScopeLevel
	}
	return OperationMsg_REPOSITORY
}

var E_Intercepted = &proto.ExtensionDesc{
	ExtendedType:  (*descriptor.ServiceOptions)(nil),
	ExtensionType: (*bool)(nil),
	Field:         82302,
	Name:          "gitaly.intercepted",
	Tag:           "varint,82302,opt,name=intercepted",
	Filename:      "lint.proto",
}

var E_OpType = &proto.ExtensionDesc{
	ExtendedType:  (*descriptor.MethodOptions)(nil),
	ExtensionType: (*OperationMsg)(nil),
	Field:         82303,
	Name:          "gitaly.op_type",
	Tag:           "bytes,82303,opt,name=op_type",
	Filename:      "lint.proto",
}

var E_Storage = &proto.ExtensionDesc{
	ExtendedType:  (*descriptor.FieldOptions)(nil),
	ExtensionType: (*bool)(nil),
	Field:         91233,
	Name:          "gitaly.storage",
	Tag:           "varint,91233,opt,name=storage",
	Filename:      "lint.proto",
}

var E_Repository = &proto.ExtensionDesc{
	ExtendedType:  (*descriptor.FieldOptions)(nil),
	ExtensionType: (*bool)(nil),
	Field:         91234,
	Name:          "gitaly.repository",
	Tag:           "varint,91234,opt,name=repository",
	Filename:      "lint.proto",
}

var E_TargetRepository = &proto.ExtensionDesc{
	ExtendedType:  (*descriptor.FieldOptions)(nil),
	ExtensionType: (*bool)(nil),
	Field:         91235,
	Name:          "gitaly.target_repository",
	Tag:           "varint,91235,opt,name=target_repository",
	Filename:      "lint.proto",
}

var E_AdditionalRepository = &proto.ExtensionDesc{
	ExtendedType:  (*descriptor.FieldOptions)(nil),
	ExtensionType: (*bool)(nil),
	Field:         91236,
	Name:          "gitaly.additional_repository",
	Tag:           "varint,91236,opt,name=additional_repository",
	Filename:      "lint.proto",
}

func init() {
	proto.RegisterEnum("gitaly.OperationMsg_Operation", OperationMsg_Operation_name, OperationMsg_Operation_value)
	proto.RegisterEnum("gitaly.OperationMsg_Scope", OperationMsg_Scope_name, OperationMsg_Scope_value)
	proto.RegisterType((*OperationMsg)(nil), "gitaly.OperationMsg")
	proto.RegisterExtension(E_Intercepted)
	proto.RegisterExtension(E_OpType)
	proto.RegisterExtension(E_Storage)
	proto.RegisterExtension(E_Repository)
	proto.RegisterExtension(E_TargetRepository)
	proto.RegisterExtension(E_AdditionalRepository)
}

func init() { proto.RegisterFile("lint.proto", fileDescriptor_1612d42a10b555ca) }

var fileDescriptor_1612d42a10b555ca = []byte{
	// 430 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x94, 0x92, 0x51, 0x6b, 0xd3, 0x50,
	0x14, 0x80, 0x4d, 0x70, 0x6d, 0x3d, 0x1d, 0x23, 0x86, 0x09, 0x65, 0xe0, 0x2c, 0x7d, 0x1a, 0x82,
	0x09, 0x76, 0x7b, 0x31, 0x3e, 0x48, 0x2d, 0x51, 0xc4, 0xb5, 0x57, 0x6e, 0x32, 0x45, 0x5f, 0x4a,
	0x9a, 0x1c, 0xaf, 0x17, 0x62, 0xcf, 0xe5, 0xe6, 0x5a, 0xe8, 0xab, 0xbf, 0xce, 0xbf, 0xa1, 0xee,
	0x77, 0xa8, 0x24, 0xb7, 0x5d, 0x0b, 0x1b, 0xa8, 0x6f, 0xb9, 0x87, 0xef, 0xfb, 0x38, 0x1c, 0x02,
	0x50, 0xca, 0x85, 0x09, 0x94, 0x26, 0x43, 0x7e, 0x4b, 0x48, 0x93, 0x95, 0xab, 0xa3, 0xbe, 0x20,
	0x12, 0x25, 0x86, 0xcd, 0x74, 0xfe, 0xe5, 0x63, 0x58, 0x60, 0x95, 0x6b, 0xa9, 0x0c, 0x69, 0x4b,
	0x0e, 0x2e, 0x1d, 0xd8, 0x67, 0x0a, 0x75, 0x66, 0x24, 0x2d, 0x26, 0x95, 0xf0, 0x03, 0x70, 0x49,
	0xf5, 0x9c, 0xbe, 0x73, 0x72, 0x30, 0x3c, 0x0e, 0x6c, 0x27, 0xd8, 0x25, 0xb6, 0x0f, 0xee, 0x92,
	0xf2, 0x9f, 0x42, 0xb7, 0xca, 0x49, 0xe1, 0xac, 0xc4, 0x25, 0x96, 0x3d, 0xb7, 0x11, 0x8f, 0x6e,
	0x14, 0x93, 0x9a, 0xe3, 0xd0, 0xe0, 0xe7, 0x35, 0x3d, 0x38, 0x85, 0x3b, 0x57, 0x84, 0xdf, 0x85,
	0xf6, 0xc5, 0xf4, 0xf5, 0x94, 0xbd, 0x9b, 0x7a, 0xb7, 0xea, 0xc7, 0xe4, 0x22, 0x1d, 0xa5, 0x8c,
	0x7b, 0x8e, 0xbf, 0x0f, 0x9d, 0xd1, 0x78, 0x1c, 0x27, 0x09, 0xe3, 0x9e, 0x3b, 0x18, 0xc2, 0x5e,
	0x53, 0xf2, 0x0f, 0x00, 0x78, 0xfc, 0x86, 0x25, 0xaf, 0x52, 0xc6, 0xdf, 0x5b, 0x27, 0x49, 0x19,
	0x1f, 0xbd, 0x8c, 0x3d, 0x77, 0x70, 0xbb, 0xe3, 0x78, 0xce, 0xc3, 0x56, 0x12, 0xf3, 0xb7, 0x31,
	0x8f, 0xc6, 0xd0, 0x95, 0x0b, 0x83, 0x3a, 0x47, 0x65, 0xb0, 0xf0, 0x1f, 0x04, 0xf6, 0x30, 0xc1,
	0xe6, 0x30, 0x41, 0x82, 0x7a, 0x29, 0x73, 0x64, 0xaa, 0x5e, 0xa5, 0xea, 0xfd, 0xfa, 0xba, 0xd7,
	0x77, 0x4e, 0x3a, 0x7c, 0xd7, 0x8a, 0x18, 0xb4, 0x49, 0xcd, 0xcc, 0x4a, 0xa1, 0x7f, 0x7c, 0x2d,
	0x30, 0x41, 0xf3, 0x89, 0x8a, 0x8d, 0xff, 0xbb, 0xf1, 0xbb, 0xc3, 0xc3, 0x9b, 0x0e, 0xc1, 0x5b,
	0xa4, 0xd2, 0x95, 0xc2, 0xe8, 0x09, 0xb4, 0x2b, 0x43, 0x3a, 0x13, 0xe8, 0xdf, 0xbf, 0x16, 0x7c,
	0x21, 0xb1, 0xbc, 0xea, 0x7d, 0xff, 0x66, 0xf7, 0xd9, 0xf0, 0xd1, 0x33, 0x00, 0x8d, 0x8a, 0x2a,
	0x69, 0x48, 0xaf, 0xfe, 0x66, 0xff, 0x58, 0xdb, 0x3b, 0x4a, 0x74, 0x0e, 0x77, 0x4d, 0xa6, 0x05,
	0x9a, 0xd9, 0xbf, 0x77, 0x7e, 0xae, 0x3b, 0x9e, 0x35, 0xf9, 0xb6, 0x96, 0xc2, 0xbd, 0xac, 0x28,
	0x64, 0x8d, 0x65, 0xe5, 0x7f, 0x14, 0x2f, 0xd7, 0xc5, 0xc3, 0xad, 0xbd, 0xad, 0x3e, 0x3f, 0xfb,
	0x30, 0x14, 0xd2, 0x94, 0xd9, 0x3c, 0xc8, 0xe9, 0x73, 0x68, 0x3f, 0x1f, 0x91, 0x16, 0xa1, 0x3d,
	0x6a, 0xb8, 0x7c, 0x7c, 0x66, 0x7f, 0xed, 0x50, 0xd0, 0x7a, 0xa6, 0xe6, 0xf3, 0x56, 0x33, 0x3a,
	0xfd, 0x13, 0x00, 0x00, 0xff, 0xff, 0x42, 0xe0, 0xf9, 0xb0, 0x11, 0x03, 0x00, 0x00,
}
