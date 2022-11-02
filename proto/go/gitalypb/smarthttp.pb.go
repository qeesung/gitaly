// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        v3.21.1
// source: smarthttp.proto

package gitalypb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// This comment is left unintentionally blank.
type InfoRefsRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// This comment is left unintentionally blank.
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// Parameters to use with git -c (key=value pairs)
	GitConfigOptions []string `protobuf:"bytes,2,rep,name=git_config_options,json=gitConfigOptions,proto3" json:"git_config_options,omitempty"`
	// Git protocol version
	GitProtocol string `protobuf:"bytes,3,opt,name=git_protocol,json=gitProtocol,proto3" json:"git_protocol,omitempty"`
}

func (x *InfoRefsRequest) Reset() {
	*x = InfoRefsRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_smarthttp_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *InfoRefsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*InfoRefsRequest) ProtoMessage() {}

func (x *InfoRefsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_smarthttp_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use InfoRefsRequest.ProtoReflect.Descriptor instead.
func (*InfoRefsRequest) Descriptor() ([]byte, []int) {
	return file_smarthttp_proto_rawDescGZIP(), []int{0}
}

func (x *InfoRefsRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *InfoRefsRequest) GetGitConfigOptions() []string {
	if x != nil {
		return x.GitConfigOptions
	}
	return nil
}

func (x *InfoRefsRequest) GetGitProtocol() string {
	if x != nil {
		return x.GitProtocol
	}
	return ""
}

// This comment is left unintentionally blank.
type InfoRefsResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// This comment is left unintentionally blank.
	Data []byte `protobuf:"bytes,1,opt,name=data,proto3" json:"data,omitempty"`
}

func (x *InfoRefsResponse) Reset() {
	*x = InfoRefsResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_smarthttp_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *InfoRefsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*InfoRefsResponse) ProtoMessage() {}

func (x *InfoRefsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_smarthttp_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use InfoRefsResponse.ProtoReflect.Descriptor instead.
func (*InfoRefsResponse) Descriptor() ([]byte, []int) {
	return file_smarthttp_proto_rawDescGZIP(), []int{1}
}

func (x *InfoRefsResponse) GetData() []byte {
	if x != nil {
		return x.Data
	}
	return nil
}

// This comment is left unintentionally blank.
type PostUploadPackRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// repository should only be present in the first message of the stream
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// Raw data to be copied to stdin of 'git upload-pack'
	Data []byte `protobuf:"bytes,2,opt,name=data,proto3" json:"data,omitempty"`
	// Parameters to use with git -c (key=value pairs)
	GitConfigOptions []string `protobuf:"bytes,3,rep,name=git_config_options,json=gitConfigOptions,proto3" json:"git_config_options,omitempty"`
	// Git protocol version
	GitProtocol string `protobuf:"bytes,4,opt,name=git_protocol,json=gitProtocol,proto3" json:"git_protocol,omitempty"`
}

func (x *PostUploadPackRequest) Reset() {
	*x = PostUploadPackRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_smarthttp_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PostUploadPackRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PostUploadPackRequest) ProtoMessage() {}

func (x *PostUploadPackRequest) ProtoReflect() protoreflect.Message {
	mi := &file_smarthttp_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PostUploadPackRequest.ProtoReflect.Descriptor instead.
func (*PostUploadPackRequest) Descriptor() ([]byte, []int) {
	return file_smarthttp_proto_rawDescGZIP(), []int{2}
}

func (x *PostUploadPackRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *PostUploadPackRequest) GetData() []byte {
	if x != nil {
		return x.Data
	}
	return nil
}

func (x *PostUploadPackRequest) GetGitConfigOptions() []string {
	if x != nil {
		return x.GitConfigOptions
	}
	return nil
}

func (x *PostUploadPackRequest) GetGitProtocol() string {
	if x != nil {
		return x.GitProtocol
	}
	return ""
}

// This comment is left unintentionally blank.
type PostUploadPackResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Raw data from stdout of 'git upload-pack'
	Data []byte `protobuf:"bytes,1,opt,name=data,proto3" json:"data,omitempty"`
}

func (x *PostUploadPackResponse) Reset() {
	*x = PostUploadPackResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_smarthttp_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PostUploadPackResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PostUploadPackResponse) ProtoMessage() {}

func (x *PostUploadPackResponse) ProtoReflect() protoreflect.Message {
	mi := &file_smarthttp_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PostUploadPackResponse.ProtoReflect.Descriptor instead.
func (*PostUploadPackResponse) Descriptor() ([]byte, []int) {
	return file_smarthttp_proto_rawDescGZIP(), []int{3}
}

func (x *PostUploadPackResponse) GetData() []byte {
	if x != nil {
		return x.Data
	}
	return nil
}

// This comment is left unintentionally blank.
type PostUploadPackWithSidechannelRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// repository should only be present in the first message of the stream
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// Parameters to use with git -c (key=value pairs)
	GitConfigOptions []string `protobuf:"bytes,2,rep,name=git_config_options,json=gitConfigOptions,proto3" json:"git_config_options,omitempty"`
	// Git protocol version
	GitProtocol string `protobuf:"bytes,3,opt,name=git_protocol,json=gitProtocol,proto3" json:"git_protocol,omitempty"`
}

func (x *PostUploadPackWithSidechannelRequest) Reset() {
	*x = PostUploadPackWithSidechannelRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_smarthttp_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PostUploadPackWithSidechannelRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PostUploadPackWithSidechannelRequest) ProtoMessage() {}

func (x *PostUploadPackWithSidechannelRequest) ProtoReflect() protoreflect.Message {
	mi := &file_smarthttp_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PostUploadPackWithSidechannelRequest.ProtoReflect.Descriptor instead.
func (*PostUploadPackWithSidechannelRequest) Descriptor() ([]byte, []int) {
	return file_smarthttp_proto_rawDescGZIP(), []int{4}
}

func (x *PostUploadPackWithSidechannelRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *PostUploadPackWithSidechannelRequest) GetGitConfigOptions() []string {
	if x != nil {
		return x.GitConfigOptions
	}
	return nil
}

func (x *PostUploadPackWithSidechannelRequest) GetGitProtocol() string {
	if x != nil {
		return x.GitProtocol
	}
	return ""
}

// This comment is left unintentionally blank.
type PostUploadPackWithSidechannelResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *PostUploadPackWithSidechannelResponse) Reset() {
	*x = PostUploadPackWithSidechannelResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_smarthttp_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PostUploadPackWithSidechannelResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PostUploadPackWithSidechannelResponse) ProtoMessage() {}

func (x *PostUploadPackWithSidechannelResponse) ProtoReflect() protoreflect.Message {
	mi := &file_smarthttp_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PostUploadPackWithSidechannelResponse.ProtoReflect.Descriptor instead.
func (*PostUploadPackWithSidechannelResponse) Descriptor() ([]byte, []int) {
	return file_smarthttp_proto_rawDescGZIP(), []int{5}
}

// This comment is left unintentionally blank.
type PostReceivePackRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// repository should only be present in the first message of the stream
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// Raw data to be copied to stdin of 'git receive-pack'
	Data []byte `protobuf:"bytes,2,opt,name=data,proto3" json:"data,omitempty"`
	// gl_id, gl_repository, and gl_username become env variables, used by the Git {pre,post}-receive
	// hooks. They should only be present in the first message of the stream.
	GlId string `protobuf:"bytes,3,opt,name=gl_id,json=glId,proto3" json:"gl_id,omitempty"`
	// This comment is left unintentionally blank.
	GlRepository string `protobuf:"bytes,4,opt,name=gl_repository,json=glRepository,proto3" json:"gl_repository,omitempty"`
	// This comment is left unintentionally blank.
	GlUsername string `protobuf:"bytes,5,opt,name=gl_username,json=glUsername,proto3" json:"gl_username,omitempty"`
	// Git protocol version
	GitProtocol string `protobuf:"bytes,6,opt,name=git_protocol,json=gitProtocol,proto3" json:"git_protocol,omitempty"`
	// Parameters to use with git -c (key=value pairs)
	GitConfigOptions []string `protobuf:"bytes,7,rep,name=git_config_options,json=gitConfigOptions,proto3" json:"git_config_options,omitempty"`
}

func (x *PostReceivePackRequest) Reset() {
	*x = PostReceivePackRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_smarthttp_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PostReceivePackRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PostReceivePackRequest) ProtoMessage() {}

func (x *PostReceivePackRequest) ProtoReflect() protoreflect.Message {
	mi := &file_smarthttp_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PostReceivePackRequest.ProtoReflect.Descriptor instead.
func (*PostReceivePackRequest) Descriptor() ([]byte, []int) {
	return file_smarthttp_proto_rawDescGZIP(), []int{6}
}

func (x *PostReceivePackRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *PostReceivePackRequest) GetData() []byte {
	if x != nil {
		return x.Data
	}
	return nil
}

func (x *PostReceivePackRequest) GetGlId() string {
	if x != nil {
		return x.GlId
	}
	return ""
}

func (x *PostReceivePackRequest) GetGlRepository() string {
	if x != nil {
		return x.GlRepository
	}
	return ""
}

func (x *PostReceivePackRequest) GetGlUsername() string {
	if x != nil {
		return x.GlUsername
	}
	return ""
}

func (x *PostReceivePackRequest) GetGitProtocol() string {
	if x != nil {
		return x.GitProtocol
	}
	return ""
}

func (x *PostReceivePackRequest) GetGitConfigOptions() []string {
	if x != nil {
		return x.GitConfigOptions
	}
	return nil
}

// This comment is left unintentionally blank.
type PostReceivePackResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Raw data from stdout of 'git receive-pack'
	Data []byte `protobuf:"bytes,1,opt,name=data,proto3" json:"data,omitempty"`
}

func (x *PostReceivePackResponse) Reset() {
	*x = PostReceivePackResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_smarthttp_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PostReceivePackResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PostReceivePackResponse) ProtoMessage() {}

func (x *PostReceivePackResponse) ProtoReflect() protoreflect.Message {
	mi := &file_smarthttp_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PostReceivePackResponse.ProtoReflect.Descriptor instead.
func (*PostReceivePackResponse) Descriptor() ([]byte, []int) {
	return file_smarthttp_proto_rawDescGZIP(), []int{7}
}

func (x *PostReceivePackResponse) GetData() []byte {
	if x != nil {
		return x.Data
	}
	return nil
}

var File_smarthttp_proto protoreflect.FileDescriptor

var file_smarthttp_proto_rawDesc = []byte{
	0x0a, 0x0f, 0x73, 0x6d, 0x61, 0x72, 0x74, 0x68, 0x74, 0x74, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x12, 0x06, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x1a, 0x0a, 0x6c, 0x69, 0x6e, 0x74, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x0c, 0x73, 0x68, 0x61, 0x72, 0x65, 0x64, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x22, 0x9c, 0x01, 0x0a, 0x0f, 0x49, 0x6e, 0x66, 0x6f, 0x52, 0x65, 0x66, 0x73,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73,
	0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69,
	0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42,
	0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72,
	0x79, 0x12, 0x2c, 0x0a, 0x12, 0x67, 0x69, 0x74, 0x5f, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x5f,
	0x6f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x09, 0x52, 0x10, 0x67,
	0x69, 0x74, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x4f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x12,
	0x21, 0x0a, 0x0c, 0x67, 0x69, 0x74, 0x5f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x18,
	0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x67, 0x69, 0x74, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x63,
	0x6f, 0x6c, 0x22, 0x26, 0x0a, 0x10, 0x49, 0x6e, 0x66, 0x6f, 0x52, 0x65, 0x66, 0x73, 0x52, 0x65,
	0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x64, 0x61, 0x74, 0x61, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0c, 0x52, 0x04, 0x64, 0x61, 0x74, 0x61, 0x22, 0xb6, 0x01, 0x0a, 0x15, 0x50,
	0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f,
	0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c,
	0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6,
	0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x12, 0x12,
	0x0a, 0x04, 0x64, 0x61, 0x74, 0x61, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x04, 0x64, 0x61,
	0x74, 0x61, 0x12, 0x2c, 0x0a, 0x12, 0x67, 0x69, 0x74, 0x5f, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67,
	0x5f, 0x6f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x09, 0x52, 0x10,
	0x67, 0x69, 0x74, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x4f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73,
	0x12, 0x21, 0x0a, 0x0c, 0x67, 0x69, 0x74, 0x5f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c,
	0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x67, 0x69, 0x74, 0x50, 0x72, 0x6f, 0x74, 0x6f,
	0x63, 0x6f, 0x6c, 0x22, 0x2c, 0x0a, 0x16, 0x50, 0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c, 0x6f, 0x61,
	0x64, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x12, 0x0a,
	0x04, 0x64, 0x61, 0x74, 0x61, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x04, 0x64, 0x61, 0x74,
	0x61, 0x22, 0xb1, 0x01, 0x0a, 0x24, 0x50, 0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64,
	0x50, 0x61, 0x63, 0x6b, 0x57, 0x69, 0x74, 0x68, 0x53, 0x69, 0x64, 0x65, 0x63, 0x68, 0x61, 0x6e,
	0x6e, 0x65, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a, 0x72, 0x65,
	0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12,
	0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f,
	0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69,
	0x74, 0x6f, 0x72, 0x79, 0x12, 0x2c, 0x0a, 0x12, 0x67, 0x69, 0x74, 0x5f, 0x63, 0x6f, 0x6e, 0x66,
	0x69, 0x67, 0x5f, 0x6f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x09,
	0x52, 0x10, 0x67, 0x69, 0x74, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x4f, 0x70, 0x74, 0x69, 0x6f,
	0x6e, 0x73, 0x12, 0x21, 0x0a, 0x0c, 0x67, 0x69, 0x74, 0x5f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63,
	0x6f, 0x6c, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x67, 0x69, 0x74, 0x50, 0x72, 0x6f,
	0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x22, 0x27, 0x0a, 0x25, 0x50, 0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c,
	0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x57, 0x69, 0x74, 0x68, 0x53, 0x69, 0x64, 0x65, 0x63,
	0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x92,
	0x02, 0x0a, 0x16, 0x50, 0x6f, 0x73, 0x74, 0x52, 0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x50, 0x61,
	0x63, 0x6b, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a, 0x72, 0x65, 0x70,
	0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e,
	0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72,
	0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74,
	0x6f, 0x72, 0x79, 0x12, 0x12, 0x0a, 0x04, 0x64, 0x61, 0x74, 0x61, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0c, 0x52, 0x04, 0x64, 0x61, 0x74, 0x61, 0x12, 0x13, 0x0a, 0x05, 0x67, 0x6c, 0x5f, 0x69, 0x64,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x67, 0x6c, 0x49, 0x64, 0x12, 0x23, 0x0a, 0x0d,
	0x67, 0x6c, 0x5f, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x04, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x0c, 0x67, 0x6c, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72,
	0x79, 0x12, 0x1f, 0x0a, 0x0b, 0x67, 0x6c, 0x5f, 0x75, 0x73, 0x65, 0x72, 0x6e, 0x61, 0x6d, 0x65,
	0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x67, 0x6c, 0x55, 0x73, 0x65, 0x72, 0x6e, 0x61,
	0x6d, 0x65, 0x12, 0x21, 0x0a, 0x0c, 0x67, 0x69, 0x74, 0x5f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63,
	0x6f, 0x6c, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x67, 0x69, 0x74, 0x50, 0x72, 0x6f,
	0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x12, 0x2c, 0x0a, 0x12, 0x67, 0x69, 0x74, 0x5f, 0x63, 0x6f, 0x6e,
	0x66, 0x69, 0x67, 0x5f, 0x6f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x18, 0x07, 0x20, 0x03, 0x28,
	0x09, 0x52, 0x10, 0x67, 0x69, 0x74, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x4f, 0x70, 0x74, 0x69,
	0x6f, 0x6e, 0x73, 0x22, 0x2d, 0x0a, 0x17, 0x50, 0x6f, 0x73, 0x74, 0x52, 0x65, 0x63, 0x65, 0x69,
	0x76, 0x65, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x12,
	0x0a, 0x04, 0x64, 0x61, 0x74, 0x61, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x04, 0x64, 0x61,
	0x74, 0x61, 0x32, 0xfd, 0x03, 0x0a, 0x10, 0x53, 0x6d, 0x61, 0x72, 0x74, 0x48, 0x54, 0x54, 0x50,
	0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x51, 0x0a, 0x12, 0x49, 0x6e, 0x66, 0x6f, 0x52,
	0x65, 0x66, 0x73, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x12, 0x17, 0x2e,
	0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x49, 0x6e, 0x66, 0x6f, 0x52, 0x65, 0x66, 0x73, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e,
	0x49, 0x6e, 0x66, 0x6f, 0x52, 0x65, 0x66, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x02, 0x30, 0x01, 0x12, 0x52, 0x0a, 0x13, 0x49, 0x6e,
	0x66, 0x6f, 0x52, 0x65, 0x66, 0x73, 0x52, 0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x50, 0x61, 0x63,
	0x6b, 0x12, 0x17, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x49, 0x6e, 0x66, 0x6f, 0x52,
	0x65, 0x66, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x67, 0x69, 0x74,
	0x61, 0x6c, 0x79, 0x2e, 0x49, 0x6e, 0x66, 0x6f, 0x52, 0x65, 0x66, 0x73, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x02, 0x30, 0x01, 0x12, 0x5b,
	0x0a, 0x0e, 0x50, 0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b,
	0x12, 0x1d, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x55, 0x70,
	0x6c, 0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a,
	0x1e, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c,
	0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22,
	0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x02, 0x28, 0x01, 0x30, 0x01, 0x12, 0x84, 0x01, 0x0a, 0x1d,
	0x50, 0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x57, 0x69,
	0x74, 0x68, 0x53, 0x69, 0x64, 0x65, 0x63, 0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x12, 0x2c, 0x2e,
	0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c, 0x6f, 0x61,
	0x64, 0x50, 0x61, 0x63, 0x6b, 0x57, 0x69, 0x74, 0x68, 0x53, 0x69, 0x64, 0x65, 0x63, 0x68, 0x61,
	0x6e, 0x6e, 0x65, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x2d, 0x2e, 0x67, 0x69,
	0x74, 0x61, 0x6c, 0x79, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x50,
	0x61, 0x63, 0x6b, 0x57, 0x69, 0x74, 0x68, 0x53, 0x69, 0x64, 0x65, 0x63, 0x68, 0x61, 0x6e, 0x6e,
	0x65, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02,
	0x08, 0x02, 0x12, 0x5e, 0x0a, 0x0f, 0x50, 0x6f, 0x73, 0x74, 0x52, 0x65, 0x63, 0x65, 0x69, 0x76,
	0x65, 0x50, 0x61, 0x63, 0x6b, 0x12, 0x1e, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x50,
	0x6f, 0x73, 0x74, 0x52, 0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1f, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x50,
	0x6f, 0x73, 0x74, 0x52, 0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65,
	0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x01, 0x28, 0x01,
	0x30, 0x01, 0x42, 0x34, 0x5a, 0x32, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2e, 0x63, 0x6f, 0x6d,
	0x2f, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2d, 0x6f, 0x72, 0x67, 0x2f, 0x67, 0x69, 0x74, 0x61,
	0x6c, 0x79, 0x2f, 0x76, 0x31, 0x35, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x67, 0x6f, 0x2f,
	0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_smarthttp_proto_rawDescOnce sync.Once
	file_smarthttp_proto_rawDescData = file_smarthttp_proto_rawDesc
)

func file_smarthttp_proto_rawDescGZIP() []byte {
	file_smarthttp_proto_rawDescOnce.Do(func() {
		file_smarthttp_proto_rawDescData = protoimpl.X.CompressGZIP(file_smarthttp_proto_rawDescData)
	})
	return file_smarthttp_proto_rawDescData
}

var file_smarthttp_proto_msgTypes = make([]protoimpl.MessageInfo, 8)
var file_smarthttp_proto_goTypes = []interface{}{
	(*InfoRefsRequest)(nil),                       // 0: gitaly.InfoRefsRequest
	(*InfoRefsResponse)(nil),                      // 1: gitaly.InfoRefsResponse
	(*PostUploadPackRequest)(nil),                 // 2: gitaly.PostUploadPackRequest
	(*PostUploadPackResponse)(nil),                // 3: gitaly.PostUploadPackResponse
	(*PostUploadPackWithSidechannelRequest)(nil),  // 4: gitaly.PostUploadPackWithSidechannelRequest
	(*PostUploadPackWithSidechannelResponse)(nil), // 5: gitaly.PostUploadPackWithSidechannelResponse
	(*PostReceivePackRequest)(nil),                // 6: gitaly.PostReceivePackRequest
	(*PostReceivePackResponse)(nil),               // 7: gitaly.PostReceivePackResponse
	(*Repository)(nil),                            // 8: gitaly.Repository
}
var file_smarthttp_proto_depIdxs = []int32{
	8, // 0: gitaly.InfoRefsRequest.repository:type_name -> gitaly.Repository
	8, // 1: gitaly.PostUploadPackRequest.repository:type_name -> gitaly.Repository
	8, // 2: gitaly.PostUploadPackWithSidechannelRequest.repository:type_name -> gitaly.Repository
	8, // 3: gitaly.PostReceivePackRequest.repository:type_name -> gitaly.Repository
	0, // 4: gitaly.SmartHTTPService.InfoRefsUploadPack:input_type -> gitaly.InfoRefsRequest
	0, // 5: gitaly.SmartHTTPService.InfoRefsReceivePack:input_type -> gitaly.InfoRefsRequest
	2, // 6: gitaly.SmartHTTPService.PostUploadPack:input_type -> gitaly.PostUploadPackRequest
	4, // 7: gitaly.SmartHTTPService.PostUploadPackWithSidechannel:input_type -> gitaly.PostUploadPackWithSidechannelRequest
	6, // 8: gitaly.SmartHTTPService.PostReceivePack:input_type -> gitaly.PostReceivePackRequest
	1, // 9: gitaly.SmartHTTPService.InfoRefsUploadPack:output_type -> gitaly.InfoRefsResponse
	1, // 10: gitaly.SmartHTTPService.InfoRefsReceivePack:output_type -> gitaly.InfoRefsResponse
	3, // 11: gitaly.SmartHTTPService.PostUploadPack:output_type -> gitaly.PostUploadPackResponse
	5, // 12: gitaly.SmartHTTPService.PostUploadPackWithSidechannel:output_type -> gitaly.PostUploadPackWithSidechannelResponse
	7, // 13: gitaly.SmartHTTPService.PostReceivePack:output_type -> gitaly.PostReceivePackResponse
	9, // [9:14] is the sub-list for method output_type
	4, // [4:9] is the sub-list for method input_type
	4, // [4:4] is the sub-list for extension type_name
	4, // [4:4] is the sub-list for extension extendee
	0, // [0:4] is the sub-list for field type_name
}

func init() { file_smarthttp_proto_init() }
func file_smarthttp_proto_init() {
	if File_smarthttp_proto != nil {
		return
	}
	file_lint_proto_init()
	file_shared_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_smarthttp_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*InfoRefsRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_smarthttp_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*InfoRefsResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_smarthttp_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PostUploadPackRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_smarthttp_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PostUploadPackResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_smarthttp_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PostUploadPackWithSidechannelRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_smarthttp_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PostUploadPackWithSidechannelResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_smarthttp_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PostReceivePackRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_smarthttp_proto_msgTypes[7].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PostReceivePackResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_smarthttp_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   8,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_smarthttp_proto_goTypes,
		DependencyIndexes: file_smarthttp_proto_depIdxs,
		MessageInfos:      file_smarthttp_proto_msgTypes,
	}.Build()
	File_smarthttp_proto = out.File
	file_smarthttp_proto_rawDesc = nil
	file_smarthttp_proto_goTypes = nil
	file_smarthttp_proto_depIdxs = nil
}
