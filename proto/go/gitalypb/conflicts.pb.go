// Code generated by protoc-gen-go. DO NOT EDIT.
// source: conflicts.proto

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

type ListConflictFilesRequest struct {
	Repository           *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	OurCommitOid         string      `protobuf:"bytes,2,opt,name=our_commit_oid,json=ourCommitOid,proto3" json:"our_commit_oid,omitempty"`
	TheirCommitOid       string      `protobuf:"bytes,3,opt,name=their_commit_oid,json=theirCommitOid,proto3" json:"their_commit_oid,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *ListConflictFilesRequest) Reset()         { *m = ListConflictFilesRequest{} }
func (m *ListConflictFilesRequest) String() string { return proto.CompactTextString(m) }
func (*ListConflictFilesRequest) ProtoMessage()    {}
func (*ListConflictFilesRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_28fc8937e7d75862, []int{0}
}

func (m *ListConflictFilesRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ListConflictFilesRequest.Unmarshal(m, b)
}
func (m *ListConflictFilesRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ListConflictFilesRequest.Marshal(b, m, deterministic)
}
func (m *ListConflictFilesRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ListConflictFilesRequest.Merge(m, src)
}
func (m *ListConflictFilesRequest) XXX_Size() int {
	return xxx_messageInfo_ListConflictFilesRequest.Size(m)
}
func (m *ListConflictFilesRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_ListConflictFilesRequest.DiscardUnknown(m)
}

var xxx_messageInfo_ListConflictFilesRequest proto.InternalMessageInfo

func (m *ListConflictFilesRequest) GetRepository() *Repository {
	if m != nil {
		return m.Repository
	}
	return nil
}

func (m *ListConflictFilesRequest) GetOurCommitOid() string {
	if m != nil {
		return m.OurCommitOid
	}
	return ""
}

func (m *ListConflictFilesRequest) GetTheirCommitOid() string {
	if m != nil {
		return m.TheirCommitOid
	}
	return ""
}

type ConflictFileHeader struct {
	CommitOid            string   `protobuf:"bytes,2,opt,name=commit_oid,json=commitOid,proto3" json:"commit_oid,omitempty"`
	TheirPath            []byte   `protobuf:"bytes,3,opt,name=their_path,json=theirPath,proto3" json:"their_path,omitempty"`
	OurPath              []byte   `protobuf:"bytes,4,opt,name=our_path,json=ourPath,proto3" json:"our_path,omitempty"`
	OurMode              int32    `protobuf:"varint,5,opt,name=our_mode,json=ourMode,proto3" json:"our_mode,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *ConflictFileHeader) Reset()         { *m = ConflictFileHeader{} }
func (m *ConflictFileHeader) String() string { return proto.CompactTextString(m) }
func (*ConflictFileHeader) ProtoMessage()    {}
func (*ConflictFileHeader) Descriptor() ([]byte, []int) {
	return fileDescriptor_28fc8937e7d75862, []int{1}
}

func (m *ConflictFileHeader) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ConflictFileHeader.Unmarshal(m, b)
}
func (m *ConflictFileHeader) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ConflictFileHeader.Marshal(b, m, deterministic)
}
func (m *ConflictFileHeader) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ConflictFileHeader.Merge(m, src)
}
func (m *ConflictFileHeader) XXX_Size() int {
	return xxx_messageInfo_ConflictFileHeader.Size(m)
}
func (m *ConflictFileHeader) XXX_DiscardUnknown() {
	xxx_messageInfo_ConflictFileHeader.DiscardUnknown(m)
}

var xxx_messageInfo_ConflictFileHeader proto.InternalMessageInfo

func (m *ConflictFileHeader) GetCommitOid() string {
	if m != nil {
		return m.CommitOid
	}
	return ""
}

func (m *ConflictFileHeader) GetTheirPath() []byte {
	if m != nil {
		return m.TheirPath
	}
	return nil
}

func (m *ConflictFileHeader) GetOurPath() []byte {
	if m != nil {
		return m.OurPath
	}
	return nil
}

func (m *ConflictFileHeader) GetOurMode() int32 {
	if m != nil {
		return m.OurMode
	}
	return 0
}

type ConflictFile struct {
	// Types that are valid to be assigned to ConflictFilePayload:
	//	*ConflictFile_Header
	//	*ConflictFile_Content
	ConflictFilePayload  isConflictFile_ConflictFilePayload `protobuf_oneof:"conflict_file_payload"`
	XXX_NoUnkeyedLiteral struct{}                           `json:"-"`
	XXX_unrecognized     []byte                             `json:"-"`
	XXX_sizecache        int32                              `json:"-"`
}

func (m *ConflictFile) Reset()         { *m = ConflictFile{} }
func (m *ConflictFile) String() string { return proto.CompactTextString(m) }
func (*ConflictFile) ProtoMessage()    {}
func (*ConflictFile) Descriptor() ([]byte, []int) {
	return fileDescriptor_28fc8937e7d75862, []int{2}
}

func (m *ConflictFile) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ConflictFile.Unmarshal(m, b)
}
func (m *ConflictFile) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ConflictFile.Marshal(b, m, deterministic)
}
func (m *ConflictFile) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ConflictFile.Merge(m, src)
}
func (m *ConflictFile) XXX_Size() int {
	return xxx_messageInfo_ConflictFile.Size(m)
}
func (m *ConflictFile) XXX_DiscardUnknown() {
	xxx_messageInfo_ConflictFile.DiscardUnknown(m)
}

var xxx_messageInfo_ConflictFile proto.InternalMessageInfo

type isConflictFile_ConflictFilePayload interface {
	isConflictFile_ConflictFilePayload()
}

type ConflictFile_Header struct {
	Header *ConflictFileHeader `protobuf:"bytes,1,opt,name=header,proto3,oneof"`
}

type ConflictFile_Content struct {
	Content []byte `protobuf:"bytes,2,opt,name=content,proto3,oneof"`
}

func (*ConflictFile_Header) isConflictFile_ConflictFilePayload() {}

func (*ConflictFile_Content) isConflictFile_ConflictFilePayload() {}

func (m *ConflictFile) GetConflictFilePayload() isConflictFile_ConflictFilePayload {
	if m != nil {
		return m.ConflictFilePayload
	}
	return nil
}

func (m *ConflictFile) GetHeader() *ConflictFileHeader {
	if x, ok := m.GetConflictFilePayload().(*ConflictFile_Header); ok {
		return x.Header
	}
	return nil
}

func (m *ConflictFile) GetContent() []byte {
	if x, ok := m.GetConflictFilePayload().(*ConflictFile_Content); ok {
		return x.Content
	}
	return nil
}

// XXX_OneofWrappers is for the internal use of the proto package.
func (*ConflictFile) XXX_OneofWrappers() []interface{} {
	return []interface{}{
		(*ConflictFile_Header)(nil),
		(*ConflictFile_Content)(nil),
	}
}

type ListConflictFilesResponse struct {
	Files                []*ConflictFile `protobuf:"bytes,1,rep,name=files,proto3" json:"files,omitempty"`
	XXX_NoUnkeyedLiteral struct{}        `json:"-"`
	XXX_unrecognized     []byte          `json:"-"`
	XXX_sizecache        int32           `json:"-"`
}

func (m *ListConflictFilesResponse) Reset()         { *m = ListConflictFilesResponse{} }
func (m *ListConflictFilesResponse) String() string { return proto.CompactTextString(m) }
func (*ListConflictFilesResponse) ProtoMessage()    {}
func (*ListConflictFilesResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_28fc8937e7d75862, []int{3}
}

func (m *ListConflictFilesResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ListConflictFilesResponse.Unmarshal(m, b)
}
func (m *ListConflictFilesResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ListConflictFilesResponse.Marshal(b, m, deterministic)
}
func (m *ListConflictFilesResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ListConflictFilesResponse.Merge(m, src)
}
func (m *ListConflictFilesResponse) XXX_Size() int {
	return xxx_messageInfo_ListConflictFilesResponse.Size(m)
}
func (m *ListConflictFilesResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_ListConflictFilesResponse.DiscardUnknown(m)
}

var xxx_messageInfo_ListConflictFilesResponse proto.InternalMessageInfo

func (m *ListConflictFilesResponse) GetFiles() []*ConflictFile {
	if m != nil {
		return m.Files
	}
	return nil
}

type ResolveConflictsRequestHeader struct {
	Repository           *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	OurCommitOid         string      `protobuf:"bytes,2,opt,name=our_commit_oid,json=ourCommitOid,proto3" json:"our_commit_oid,omitempty"`
	TargetRepository     *Repository `protobuf:"bytes,3,opt,name=target_repository,json=targetRepository,proto3" json:"target_repository,omitempty"`
	TheirCommitOid       string      `protobuf:"bytes,4,opt,name=their_commit_oid,json=theirCommitOid,proto3" json:"their_commit_oid,omitempty"`
	SourceBranch         []byte      `protobuf:"bytes,5,opt,name=source_branch,json=sourceBranch,proto3" json:"source_branch,omitempty"`
	TargetBranch         []byte      `protobuf:"bytes,6,opt,name=target_branch,json=targetBranch,proto3" json:"target_branch,omitempty"`
	CommitMessage        []byte      `protobuf:"bytes,7,opt,name=commit_message,json=commitMessage,proto3" json:"commit_message,omitempty"`
	User                 *User       `protobuf:"bytes,8,opt,name=user,proto3" json:"user,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *ResolveConflictsRequestHeader) Reset()         { *m = ResolveConflictsRequestHeader{} }
func (m *ResolveConflictsRequestHeader) String() string { return proto.CompactTextString(m) }
func (*ResolveConflictsRequestHeader) ProtoMessage()    {}
func (*ResolveConflictsRequestHeader) Descriptor() ([]byte, []int) {
	return fileDescriptor_28fc8937e7d75862, []int{4}
}

func (m *ResolveConflictsRequestHeader) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ResolveConflictsRequestHeader.Unmarshal(m, b)
}
func (m *ResolveConflictsRequestHeader) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ResolveConflictsRequestHeader.Marshal(b, m, deterministic)
}
func (m *ResolveConflictsRequestHeader) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ResolveConflictsRequestHeader.Merge(m, src)
}
func (m *ResolveConflictsRequestHeader) XXX_Size() int {
	return xxx_messageInfo_ResolveConflictsRequestHeader.Size(m)
}
func (m *ResolveConflictsRequestHeader) XXX_DiscardUnknown() {
	xxx_messageInfo_ResolveConflictsRequestHeader.DiscardUnknown(m)
}

var xxx_messageInfo_ResolveConflictsRequestHeader proto.InternalMessageInfo

func (m *ResolveConflictsRequestHeader) GetRepository() *Repository {
	if m != nil {
		return m.Repository
	}
	return nil
}

func (m *ResolveConflictsRequestHeader) GetOurCommitOid() string {
	if m != nil {
		return m.OurCommitOid
	}
	return ""
}

func (m *ResolveConflictsRequestHeader) GetTargetRepository() *Repository {
	if m != nil {
		return m.TargetRepository
	}
	return nil
}

func (m *ResolveConflictsRequestHeader) GetTheirCommitOid() string {
	if m != nil {
		return m.TheirCommitOid
	}
	return ""
}

func (m *ResolveConflictsRequestHeader) GetSourceBranch() []byte {
	if m != nil {
		return m.SourceBranch
	}
	return nil
}

func (m *ResolveConflictsRequestHeader) GetTargetBranch() []byte {
	if m != nil {
		return m.TargetBranch
	}
	return nil
}

func (m *ResolveConflictsRequestHeader) GetCommitMessage() []byte {
	if m != nil {
		return m.CommitMessage
	}
	return nil
}

func (m *ResolveConflictsRequestHeader) GetUser() *User {
	if m != nil {
		return m.User
	}
	return nil
}

type ResolveConflictsRequest struct {
	// Types that are valid to be assigned to ResolveConflictsRequestPayload:
	//	*ResolveConflictsRequest_Header
	//	*ResolveConflictsRequest_FilesJson
	ResolveConflictsRequestPayload isResolveConflictsRequest_ResolveConflictsRequestPayload `protobuf_oneof:"resolve_conflicts_request_payload"`
	XXX_NoUnkeyedLiteral           struct{}                                                 `json:"-"`
	XXX_unrecognized               []byte                                                   `json:"-"`
	XXX_sizecache                  int32                                                    `json:"-"`
}

func (m *ResolveConflictsRequest) Reset()         { *m = ResolveConflictsRequest{} }
func (m *ResolveConflictsRequest) String() string { return proto.CompactTextString(m) }
func (*ResolveConflictsRequest) ProtoMessage()    {}
func (*ResolveConflictsRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_28fc8937e7d75862, []int{5}
}

func (m *ResolveConflictsRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ResolveConflictsRequest.Unmarshal(m, b)
}
func (m *ResolveConflictsRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ResolveConflictsRequest.Marshal(b, m, deterministic)
}
func (m *ResolveConflictsRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ResolveConflictsRequest.Merge(m, src)
}
func (m *ResolveConflictsRequest) XXX_Size() int {
	return xxx_messageInfo_ResolveConflictsRequest.Size(m)
}
func (m *ResolveConflictsRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_ResolveConflictsRequest.DiscardUnknown(m)
}

var xxx_messageInfo_ResolveConflictsRequest proto.InternalMessageInfo

type isResolveConflictsRequest_ResolveConflictsRequestPayload interface {
	isResolveConflictsRequest_ResolveConflictsRequestPayload()
}

type ResolveConflictsRequest_Header struct {
	Header *ResolveConflictsRequestHeader `protobuf:"bytes,1,opt,name=header,proto3,oneof"`
}

type ResolveConflictsRequest_FilesJson struct {
	FilesJson []byte `protobuf:"bytes,2,opt,name=files_json,json=filesJson,proto3,oneof"`
}

func (*ResolveConflictsRequest_Header) isResolveConflictsRequest_ResolveConflictsRequestPayload() {}

func (*ResolveConflictsRequest_FilesJson) isResolveConflictsRequest_ResolveConflictsRequestPayload() {}

func (m *ResolveConflictsRequest) GetResolveConflictsRequestPayload() isResolveConflictsRequest_ResolveConflictsRequestPayload {
	if m != nil {
		return m.ResolveConflictsRequestPayload
	}
	return nil
}

func (m *ResolveConflictsRequest) GetHeader() *ResolveConflictsRequestHeader {
	if x, ok := m.GetResolveConflictsRequestPayload().(*ResolveConflictsRequest_Header); ok {
		return x.Header
	}
	return nil
}

func (m *ResolveConflictsRequest) GetFilesJson() []byte {
	if x, ok := m.GetResolveConflictsRequestPayload().(*ResolveConflictsRequest_FilesJson); ok {
		return x.FilesJson
	}
	return nil
}

// XXX_OneofWrappers is for the internal use of the proto package.
func (*ResolveConflictsRequest) XXX_OneofWrappers() []interface{} {
	return []interface{}{
		(*ResolveConflictsRequest_Header)(nil),
		(*ResolveConflictsRequest_FilesJson)(nil),
	}
}

type ResolveConflictsResponse struct {
	ResolutionError      string   `protobuf:"bytes,1,opt,name=resolution_error,json=resolutionError,proto3" json:"resolution_error,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *ResolveConflictsResponse) Reset()         { *m = ResolveConflictsResponse{} }
func (m *ResolveConflictsResponse) String() string { return proto.CompactTextString(m) }
func (*ResolveConflictsResponse) ProtoMessage()    {}
func (*ResolveConflictsResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_28fc8937e7d75862, []int{6}
}

func (m *ResolveConflictsResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ResolveConflictsResponse.Unmarshal(m, b)
}
func (m *ResolveConflictsResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ResolveConflictsResponse.Marshal(b, m, deterministic)
}
func (m *ResolveConflictsResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ResolveConflictsResponse.Merge(m, src)
}
func (m *ResolveConflictsResponse) XXX_Size() int {
	return xxx_messageInfo_ResolveConflictsResponse.Size(m)
}
func (m *ResolveConflictsResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_ResolveConflictsResponse.DiscardUnknown(m)
}

var xxx_messageInfo_ResolveConflictsResponse proto.InternalMessageInfo

func (m *ResolveConflictsResponse) GetResolutionError() string {
	if m != nil {
		return m.ResolutionError
	}
	return ""
}

func init() {
	proto.RegisterType((*ListConflictFilesRequest)(nil), "gitaly.ListConflictFilesRequest")
	proto.RegisterType((*ConflictFileHeader)(nil), "gitaly.ConflictFileHeader")
	proto.RegisterType((*ConflictFile)(nil), "gitaly.ConflictFile")
	proto.RegisterType((*ListConflictFilesResponse)(nil), "gitaly.ListConflictFilesResponse")
	proto.RegisterType((*ResolveConflictsRequestHeader)(nil), "gitaly.ResolveConflictsRequestHeader")
	proto.RegisterType((*ResolveConflictsRequest)(nil), "gitaly.ResolveConflictsRequest")
	proto.RegisterType((*ResolveConflictsResponse)(nil), "gitaly.ResolveConflictsResponse")
}

func init() { proto.RegisterFile("conflicts.proto", fileDescriptor_28fc8937e7d75862) }

var fileDescriptor_28fc8937e7d75862 = []byte{
	// 630 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xb4, 0x54, 0x41, 0x6f, 0xd3, 0x30,
	0x14, 0x9e, 0xbb, 0xae, 0x6b, 0xdf, 0xb2, 0xad, 0xb3, 0x40, 0xcb, 0x2a, 0x4d, 0xcb, 0x32, 0x26,
	0x05, 0x04, 0xed, 0x34, 0x38, 0x70, 0x9b, 0xd4, 0x69, 0x30, 0x4d, 0x4c, 0x20, 0x23, 0x2e, 0x5c,
	0xa2, 0x34, 0xf1, 0x1a, 0xa3, 0x34, 0x0e, 0xb6, 0x33, 0xa9, 0x7f, 0x82, 0x2b, 0x1c, 0x39, 0xf0,
	0x5b, 0xf8, 0x0b, 0xfc, 0x17, 0x24, 0x24, 0x54, 0x3b, 0xc9, 0xba, 0xb5, 0x1d, 0x27, 0x6e, 0xf1,
	0xf7, 0xbe, 0x7c, 0xef, 0xf3, 0x7b, 0x9f, 0x0c, 0x9b, 0x21, 0x4f, 0xaf, 0x12, 0x16, 0x2a, 0xd9,
	0xcd, 0x04, 0x57, 0x1c, 0x37, 0x86, 0x4c, 0x05, 0xc9, 0xb8, 0x03, 0x09, 0x4b, 0x95, 0xc1, 0x3a,
	0x96, 0x8c, 0x03, 0x41, 0x23, 0x73, 0x72, 0x7f, 0x20, 0xb0, 0xdf, 0x30, 0xa9, 0x4e, 0x8b, 0x3f,
	0x5f, 0xb1, 0x84, 0x4a, 0x42, 0x3f, 0xe7, 0x54, 0x2a, 0xfc, 0x12, 0x40, 0xd0, 0x8c, 0x4b, 0xa6,
	0xb8, 0x18, 0xdb, 0xc8, 0x41, 0xde, 0xda, 0x31, 0xee, 0x1a, 0xcd, 0x2e, 0xa9, 0x2a, 0xfd, 0xfa,
	0xb7, 0x9f, 0x4f, 0x11, 0x99, 0xe2, 0xe2, 0x47, 0xb0, 0xc1, 0x73, 0xe1, 0x87, 0x7c, 0x34, 0x62,
	0xca, 0xe7, 0x2c, 0xb2, 0x6b, 0x0e, 0xf2, 0x5a, 0xc4, 0xe2, 0xb9, 0x38, 0xd5, 0xe0, 0x5b, 0x16,
	0x61, 0x0f, 0xda, 0x2a, 0xa6, 0xec, 0x16, 0x6f, 0x59, 0xf3, 0x36, 0x34, 0x5e, 0x31, 0xdd, 0x2f,
	0x08, 0xf0, 0xb4, 0xc5, 0x73, 0x1a, 0x44, 0x54, 0xe0, 0x5d, 0x80, 0x99, 0x16, 0xad, 0xb0, 0xd2,
	0xdf, 0x05, 0x30, 0xfa, 0x59, 0xa0, 0x62, 0xad, 0x6c, 0x91, 0x96, 0x46, 0xde, 0x05, 0x2a, 0xc6,
	0x3b, 0xd0, 0x9c, 0x98, 0xd4, 0xc5, 0xba, 0x2e, 0xae, 0xf2, 0xfc, 0x56, 0x69, 0xc4, 0x23, 0x6a,
	0xaf, 0x38, 0xc8, 0x5b, 0xd1, 0xa5, 0x4b, 0x1e, 0xd1, 0x8b, 0x7a, 0x13, 0xb5, 0x6b, 0xee, 0x18,
	0xac, 0x69, 0x3f, 0xf8, 0x05, 0x34, 0x62, 0xed, 0xa9, 0x18, 0x53, 0xa7, 0x1c, 0xd3, 0xac, 0xeb,
	0xf3, 0x25, 0x52, 0x70, 0x71, 0x07, 0x56, 0x43, 0x9e, 0x2a, 0x9a, 0x2a, 0x6d, 0xde, 0x3a, 0x5f,
	0x22, 0x25, 0xd0, 0xdf, 0x86, 0x87, 0xe5, 0x3a, 0xfd, 0x2b, 0x96, 0x50, 0x3f, 0x0b, 0xc6, 0x09,
	0x0f, 0x22, 0xf7, 0x35, 0xec, 0xcc, 0xd9, 0x98, 0xcc, 0x78, 0x2a, 0x29, 0x7e, 0x02, 0x2b, 0x13,
	0xb2, 0xb4, 0x91, 0xb3, 0xec, 0xad, 0x1d, 0x3f, 0x98, 0x67, 0x83, 0x18, 0x8a, 0xfb, 0xa7, 0x06,
	0xbb, 0x84, 0x4a, 0x9e, 0x5c, 0xd3, 0xb2, 0x5c, 0xae, 0xbe, 0x98, 0xef, 0xff, 0x0e, 0xc0, 0x09,
	0x6c, 0xa9, 0x40, 0x0c, 0xa9, 0xf2, 0xa7, 0xda, 0x2c, 0x2f, 0x6a, 0x43, 0xda, 0x86, 0x7c, 0x83,
	0xcc, 0x4d, 0x50, 0x7d, 0x5e, 0x82, 0xf0, 0x01, 0xac, 0x4b, 0x9e, 0x8b, 0x90, 0xfa, 0x03, 0x11,
	0xa4, 0x61, 0xac, 0xd7, 0x6a, 0x11, 0xcb, 0x80, 0x7d, 0x8d, 0x4d, 0x48, 0x85, 0x9f, 0x82, 0xd4,
	0x30, 0x24, 0x03, 0x16, 0xa4, 0x43, 0xd8, 0x28, 0xba, 0x8d, 0xa8, 0x94, 0xc1, 0x90, 0xda, 0xab,
	0x9a, 0xb5, 0x6e, 0xd0, 0x4b, 0x03, 0x62, 0x07, 0xea, 0xb9, 0xa4, 0xc2, 0x6e, 0xea, 0xeb, 0x58,
	0xe5, 0x75, 0x3e, 0x48, 0x2a, 0x88, 0xae, 0xb8, 0xdf, 0x11, 0x6c, 0x2f, 0x98, 0x3f, 0x3e, 0xb9,
	0x93, 0xa7, 0xc3, 0x9b, 0x71, 0xdc, 0xb3, 0xb0, 0xa9, 0x68, 0xed, 0x01, 0xe8, 0x2d, 0xfb, 0x9f,
	0x24, 0x4f, 0xab, 0x74, 0xb5, 0x34, 0x76, 0x21, 0x79, 0xda, 0x3f, 0x80, 0x7d, 0x61, 0xb4, 0xfc,
	0xea, 0xd9, 0xf0, 0x85, 0x51, 0xab, 0xb2, 0x76, 0x06, 0xf6, 0x6c, 0xc3, 0x22, 0x6a, 0x8f, 0xa1,
	0xad, 0x05, 0x72, 0xc5, 0x78, 0xea, 0x53, 0x21, 0xb8, 0x31, 0xdb, 0x22, 0x9b, 0x37, 0xf8, 0xd9,
	0x04, 0x3e, 0xfe, 0x85, 0xa0, 0x5d, 0x09, 0xbc, 0xa7, 0xe2, 0x9a, 0x85, 0x14, 0x0f, 0x60, 0x6b,
	0x26, 0xc7, 0xd8, 0x29, 0xef, 0xb9, 0xe8, 0x51, 0xea, 0xec, 0xdf, 0xc3, 0x30, 0xce, 0xdc, 0xc6,
	0xef, 0xaf, 0x5e, 0xad, 0x59, 0x3b, 0x42, 0xd8, 0x87, 0xf6, 0x5d, 0xff, 0x78, 0xef, 0x1f, 0xa3,
	0xec, 0x38, 0x8b, 0x09, 0xb7, 0x1a, 0x20, 0x0f, 0xf5, 0x8f, 0x3e, 0x4e, 0xc8, 0x49, 0x30, 0xe8,
	0x86, 0x7c, 0xd4, 0x33, 0x9f, 0xcf, 0xb8, 0x18, 0xf6, 0x8c, 0x44, 0x4f, 0xbf, 0xb2, 0xbd, 0x21,
	0x2f, 0xce, 0xd9, 0x60, 0xd0, 0xd0, 0xd0, 0xf3, 0xbf, 0x01, 0x00, 0x00, 0xff, 0xff, 0x3e, 0x26,
	0x14, 0x93, 0xad, 0x05, 0x00, 0x00,
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// ConflictsServiceClient is the client API for ConflictsService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type ConflictsServiceClient interface {
	ListConflictFiles(ctx context.Context, in *ListConflictFilesRequest, opts ...grpc.CallOption) (ConflictsService_ListConflictFilesClient, error)
	ResolveConflicts(ctx context.Context, opts ...grpc.CallOption) (ConflictsService_ResolveConflictsClient, error)
}

type conflictsServiceClient struct {
	cc *grpc.ClientConn
}

func NewConflictsServiceClient(cc *grpc.ClientConn) ConflictsServiceClient {
	return &conflictsServiceClient{cc}
}

func (c *conflictsServiceClient) ListConflictFiles(ctx context.Context, in *ListConflictFilesRequest, opts ...grpc.CallOption) (ConflictsService_ListConflictFilesClient, error) {
	stream, err := c.cc.NewStream(ctx, &_ConflictsService_serviceDesc.Streams[0], "/gitaly.ConflictsService/ListConflictFiles", opts...)
	if err != nil {
		return nil, err
	}
	x := &conflictsServiceListConflictFilesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type ConflictsService_ListConflictFilesClient interface {
	Recv() (*ListConflictFilesResponse, error)
	grpc.ClientStream
}

type conflictsServiceListConflictFilesClient struct {
	grpc.ClientStream
}

func (x *conflictsServiceListConflictFilesClient) Recv() (*ListConflictFilesResponse, error) {
	m := new(ListConflictFilesResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *conflictsServiceClient) ResolveConflicts(ctx context.Context, opts ...grpc.CallOption) (ConflictsService_ResolveConflictsClient, error) {
	stream, err := c.cc.NewStream(ctx, &_ConflictsService_serviceDesc.Streams[1], "/gitaly.ConflictsService/ResolveConflicts", opts...)
	if err != nil {
		return nil, err
	}
	x := &conflictsServiceResolveConflictsClient{stream}
	return x, nil
}

type ConflictsService_ResolveConflictsClient interface {
	Send(*ResolveConflictsRequest) error
	CloseAndRecv() (*ResolveConflictsResponse, error)
	grpc.ClientStream
}

type conflictsServiceResolveConflictsClient struct {
	grpc.ClientStream
}

func (x *conflictsServiceResolveConflictsClient) Send(m *ResolveConflictsRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *conflictsServiceResolveConflictsClient) CloseAndRecv() (*ResolveConflictsResponse, error) {
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	m := new(ResolveConflictsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// ConflictsServiceServer is the server API for ConflictsService service.
type ConflictsServiceServer interface {
	ListConflictFiles(*ListConflictFilesRequest, ConflictsService_ListConflictFilesServer) error
	ResolveConflicts(ConflictsService_ResolveConflictsServer) error
}

// UnimplementedConflictsServiceServer can be embedded to have forward compatible implementations.
type UnimplementedConflictsServiceServer struct {
}

func (*UnimplementedConflictsServiceServer) ListConflictFiles(req *ListConflictFilesRequest, srv ConflictsService_ListConflictFilesServer) error {
	return status.Errorf(codes.Unimplemented, "method ListConflictFiles not implemented")
}
func (*UnimplementedConflictsServiceServer) ResolveConflicts(srv ConflictsService_ResolveConflictsServer) error {
	return status.Errorf(codes.Unimplemented, "method ResolveConflicts not implemented")
}

func RegisterConflictsServiceServer(s *grpc.Server, srv ConflictsServiceServer) {
	s.RegisterService(&_ConflictsService_serviceDesc, srv)
}

func _ConflictsService_ListConflictFiles_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(ListConflictFilesRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(ConflictsServiceServer).ListConflictFiles(m, &conflictsServiceListConflictFilesServer{stream})
}

type ConflictsService_ListConflictFilesServer interface {
	Send(*ListConflictFilesResponse) error
	grpc.ServerStream
}

type conflictsServiceListConflictFilesServer struct {
	grpc.ServerStream
}

func (x *conflictsServiceListConflictFilesServer) Send(m *ListConflictFilesResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _ConflictsService_ResolveConflicts_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(ConflictsServiceServer).ResolveConflicts(&conflictsServiceResolveConflictsServer{stream})
}

type ConflictsService_ResolveConflictsServer interface {
	SendAndClose(*ResolveConflictsResponse) error
	Recv() (*ResolveConflictsRequest, error)
	grpc.ServerStream
}

type conflictsServiceResolveConflictsServer struct {
	grpc.ServerStream
}

func (x *conflictsServiceResolveConflictsServer) SendAndClose(m *ResolveConflictsResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *conflictsServiceResolveConflictsServer) Recv() (*ResolveConflictsRequest, error) {
	m := new(ResolveConflictsRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

var _ConflictsService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.ConflictsService",
	HandlerType: (*ConflictsServiceServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "ListConflictFiles",
			Handler:       _ConflictsService_ListConflictFiles_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "ResolveConflicts",
			Handler:       _ConflictsService_ResolveConflicts_Handler,
			ClientStreams: true,
		},
	},
	Metadata: "conflicts.proto",
}
