// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.26.0
// 	protoc        v3.15.8
// source: ssh.proto

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

type SSHUploadPackRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// 'repository' must be present in the first message.
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// A chunk of raw data to be copied to 'git upload-pack' standard input
	Stdin []byte `protobuf:"bytes,2,opt,name=stdin,proto3" json:"stdin,omitempty"`
	// Parameters to use with git -c (key=value pairs)
	GitConfigOptions []string `protobuf:"bytes,4,rep,name=git_config_options,json=gitConfigOptions,proto3" json:"git_config_options,omitempty"`
	// Git protocol version
	GitProtocol string `protobuf:"bytes,5,opt,name=git_protocol,json=gitProtocol,proto3" json:"git_protocol,omitempty"`
}

func (x *SSHUploadPackRequest) Reset() {
	*x = SSHUploadPackRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_ssh_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SSHUploadPackRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SSHUploadPackRequest) ProtoMessage() {}

func (x *SSHUploadPackRequest) ProtoReflect() protoreflect.Message {
	mi := &file_ssh_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SSHUploadPackRequest.ProtoReflect.Descriptor instead.
func (*SSHUploadPackRequest) Descriptor() ([]byte, []int) {
	return file_ssh_proto_rawDescGZIP(), []int{0}
}

func (x *SSHUploadPackRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *SSHUploadPackRequest) GetStdin() []byte {
	if x != nil {
		return x.Stdin
	}
	return nil
}

func (x *SSHUploadPackRequest) GetGitConfigOptions() []string {
	if x != nil {
		return x.GitConfigOptions
	}
	return nil
}

func (x *SSHUploadPackRequest) GetGitProtocol() string {
	if x != nil {
		return x.GitProtocol
	}
	return ""
}

type SSHUploadPackResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// A chunk of raw data from 'git upload-pack' standard output
	Stdout []byte `protobuf:"bytes,1,opt,name=stdout,proto3" json:"stdout,omitempty"`
	// A chunk of raw data from 'git upload-pack' standard error
	Stderr []byte `protobuf:"bytes,2,opt,name=stderr,proto3" json:"stderr,omitempty"`
	// This field may be nil. This is intentional: only when the remote
	// command has finished can we return its exit status.
	ExitStatus *ExitStatus `protobuf:"bytes,3,opt,name=exit_status,json=exitStatus,proto3" json:"exit_status,omitempty"`
}

func (x *SSHUploadPackResponse) Reset() {
	*x = SSHUploadPackResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_ssh_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SSHUploadPackResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SSHUploadPackResponse) ProtoMessage() {}

func (x *SSHUploadPackResponse) ProtoReflect() protoreflect.Message {
	mi := &file_ssh_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SSHUploadPackResponse.ProtoReflect.Descriptor instead.
func (*SSHUploadPackResponse) Descriptor() ([]byte, []int) {
	return file_ssh_proto_rawDescGZIP(), []int{1}
}

func (x *SSHUploadPackResponse) GetStdout() []byte {
	if x != nil {
		return x.Stdout
	}
	return nil
}

func (x *SSHUploadPackResponse) GetStderr() []byte {
	if x != nil {
		return x.Stderr
	}
	return nil
}

func (x *SSHUploadPackResponse) GetExitStatus() *ExitStatus {
	if x != nil {
		return x.ExitStatus
	}
	return nil
}

type SSHReceivePackRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// 'repository' must be present in the first message.
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// A chunk of raw data to be copied to 'git upload-pack' standard input
	Stdin []byte `protobuf:"bytes,2,opt,name=stdin,proto3" json:"stdin,omitempty"`
	// Contents of GL_ID, GL_REPOSITORY, and GL_USERNAME environment variables
	// for 'git receive-pack'
	GlId         string `protobuf:"bytes,3,opt,name=gl_id,json=glId,proto3" json:"gl_id,omitempty"`
	GlRepository string `protobuf:"bytes,4,opt,name=gl_repository,json=glRepository,proto3" json:"gl_repository,omitempty"`
	GlUsername   string `protobuf:"bytes,5,opt,name=gl_username,json=glUsername,proto3" json:"gl_username,omitempty"`
	// Git protocol version
	GitProtocol string `protobuf:"bytes,6,opt,name=git_protocol,json=gitProtocol,proto3" json:"git_protocol,omitempty"`
	// Parameters to use with git -c (key=value pairs)
	GitConfigOptions []string `protobuf:"bytes,7,rep,name=git_config_options,json=gitConfigOptions,proto3" json:"git_config_options,omitempty"`
}

func (x *SSHReceivePackRequest) Reset() {
	*x = SSHReceivePackRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_ssh_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SSHReceivePackRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SSHReceivePackRequest) ProtoMessage() {}

func (x *SSHReceivePackRequest) ProtoReflect() protoreflect.Message {
	mi := &file_ssh_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SSHReceivePackRequest.ProtoReflect.Descriptor instead.
func (*SSHReceivePackRequest) Descriptor() ([]byte, []int) {
	return file_ssh_proto_rawDescGZIP(), []int{2}
}

func (x *SSHReceivePackRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *SSHReceivePackRequest) GetStdin() []byte {
	if x != nil {
		return x.Stdin
	}
	return nil
}

func (x *SSHReceivePackRequest) GetGlId() string {
	if x != nil {
		return x.GlId
	}
	return ""
}

func (x *SSHReceivePackRequest) GetGlRepository() string {
	if x != nil {
		return x.GlRepository
	}
	return ""
}

func (x *SSHReceivePackRequest) GetGlUsername() string {
	if x != nil {
		return x.GlUsername
	}
	return ""
}

func (x *SSHReceivePackRequest) GetGitProtocol() string {
	if x != nil {
		return x.GitProtocol
	}
	return ""
}

func (x *SSHReceivePackRequest) GetGitConfigOptions() []string {
	if x != nil {
		return x.GitConfigOptions
	}
	return nil
}

type SSHReceivePackResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// A chunk of raw data from 'git receive-pack' standard output
	Stdout []byte `protobuf:"bytes,1,opt,name=stdout,proto3" json:"stdout,omitempty"`
	// A chunk of raw data from 'git receive-pack' standard error
	Stderr []byte `protobuf:"bytes,2,opt,name=stderr,proto3" json:"stderr,omitempty"`
	// This field may be nil. This is intentional: only when the remote
	// command has finished can we return its exit status.
	ExitStatus *ExitStatus `protobuf:"bytes,3,opt,name=exit_status,json=exitStatus,proto3" json:"exit_status,omitempty"`
}

func (x *SSHReceivePackResponse) Reset() {
	*x = SSHReceivePackResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_ssh_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SSHReceivePackResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SSHReceivePackResponse) ProtoMessage() {}

func (x *SSHReceivePackResponse) ProtoReflect() protoreflect.Message {
	mi := &file_ssh_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SSHReceivePackResponse.ProtoReflect.Descriptor instead.
func (*SSHReceivePackResponse) Descriptor() ([]byte, []int) {
	return file_ssh_proto_rawDescGZIP(), []int{3}
}

func (x *SSHReceivePackResponse) GetStdout() []byte {
	if x != nil {
		return x.Stdout
	}
	return nil
}

func (x *SSHReceivePackResponse) GetStderr() []byte {
	if x != nil {
		return x.Stderr
	}
	return nil
}

func (x *SSHReceivePackResponse) GetExitStatus() *ExitStatus {
	if x != nil {
		return x.ExitStatus
	}
	return nil
}

type SSHUploadArchiveRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// 'repository' must be present in the first message.
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// A chunk of raw data to be copied to 'git upload-archive' standard input
	Stdin []byte `protobuf:"bytes,2,opt,name=stdin,proto3" json:"stdin,omitempty"`
}

func (x *SSHUploadArchiveRequest) Reset() {
	*x = SSHUploadArchiveRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_ssh_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SSHUploadArchiveRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SSHUploadArchiveRequest) ProtoMessage() {}

func (x *SSHUploadArchiveRequest) ProtoReflect() protoreflect.Message {
	mi := &file_ssh_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SSHUploadArchiveRequest.ProtoReflect.Descriptor instead.
func (*SSHUploadArchiveRequest) Descriptor() ([]byte, []int) {
	return file_ssh_proto_rawDescGZIP(), []int{4}
}

func (x *SSHUploadArchiveRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *SSHUploadArchiveRequest) GetStdin() []byte {
	if x != nil {
		return x.Stdin
	}
	return nil
}

type SSHUploadArchiveResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// A chunk of raw data from 'git upload-archive' standard output
	Stdout []byte `protobuf:"bytes,1,opt,name=stdout,proto3" json:"stdout,omitempty"`
	// A chunk of raw data from 'git upload-archive' standard error
	Stderr []byte `protobuf:"bytes,2,opt,name=stderr,proto3" json:"stderr,omitempty"`
	// This value will only be set on the last message
	ExitStatus *ExitStatus `protobuf:"bytes,3,opt,name=exit_status,json=exitStatus,proto3" json:"exit_status,omitempty"`
}

func (x *SSHUploadArchiveResponse) Reset() {
	*x = SSHUploadArchiveResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_ssh_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SSHUploadArchiveResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SSHUploadArchiveResponse) ProtoMessage() {}

func (x *SSHUploadArchiveResponse) ProtoReflect() protoreflect.Message {
	mi := &file_ssh_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SSHUploadArchiveResponse.ProtoReflect.Descriptor instead.
func (*SSHUploadArchiveResponse) Descriptor() ([]byte, []int) {
	return file_ssh_proto_rawDescGZIP(), []int{5}
}

func (x *SSHUploadArchiveResponse) GetStdout() []byte {
	if x != nil {
		return x.Stdout
	}
	return nil
}

func (x *SSHUploadArchiveResponse) GetStderr() []byte {
	if x != nil {
		return x.Stderr
	}
	return nil
}

func (x *SSHUploadArchiveResponse) GetExitStatus() *ExitStatus {
	if x != nil {
		return x.ExitStatus
	}
	return nil
}

var File_ssh_proto protoreflect.FileDescriptor

var file_ssh_proto_rawDesc = []byte{
	0x0a, 0x09, 0x73, 0x73, 0x68, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x06, 0x67, 0x69, 0x74,
	0x61, 0x6c, 0x79, 0x1a, 0x0a, 0x6c, 0x69, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a,
	0x0c, 0x73, 0x68, 0x61, 0x72, 0x65, 0x64, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xd4, 0x01,
	0x0a, 0x14, 0x53, 0x53, 0x48, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69,
	0x74, 0x6f, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74,
	0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04,
	0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79,
	0x12, 0x14, 0x0a, 0x05, 0x73, 0x74, 0x64, 0x69, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52,
	0x05, 0x73, 0x74, 0x64, 0x69, 0x6e, 0x12, 0x2c, 0x0a, 0x12, 0x67, 0x69, 0x74, 0x5f, 0x63, 0x6f,
	0x6e, 0x66, 0x69, 0x67, 0x5f, 0x6f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x18, 0x04, 0x20, 0x03,
	0x28, 0x09, 0x52, 0x10, 0x67, 0x69, 0x74, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x4f, 0x70, 0x74,
	0x69, 0x6f, 0x6e, 0x73, 0x12, 0x21, 0x0a, 0x0c, 0x67, 0x69, 0x74, 0x5f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x63, 0x6f, 0x6c, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x67, 0x69, 0x74, 0x50,
	0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x4a, 0x04, 0x08, 0x03, 0x10, 0x04, 0x52, 0x15, 0x67,
	0x69, 0x74, 0x5f, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x5f, 0x70, 0x61, 0x72, 0x61, 0x6d, 0x65,
	0x74, 0x65, 0x72, 0x73, 0x22, 0x7c, 0x0a, 0x15, 0x53, 0x53, 0x48, 0x55, 0x70, 0x6c, 0x6f, 0x61,
	0x64, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x16, 0x0a,
	0x06, 0x73, 0x74, 0x64, 0x6f, 0x75, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x06, 0x73,
	0x74, 0x64, 0x6f, 0x75, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x74, 0x64, 0x65, 0x72, 0x72, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x06, 0x73, 0x74, 0x64, 0x65, 0x72, 0x72, 0x12, 0x33, 0x0a,
	0x0b, 0x65, 0x78, 0x69, 0x74, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x03, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x45, 0x78, 0x69, 0x74,
	0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x52, 0x0a, 0x65, 0x78, 0x69, 0x74, 0x53, 0x74, 0x61, 0x74,
	0x75, 0x73, 0x22, 0x93, 0x02, 0x0a, 0x15, 0x53, 0x53, 0x48, 0x52, 0x65, 0x63, 0x65, 0x69, 0x76,
	0x65, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a,
	0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69,
	0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f,
	0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x73, 0x74, 0x64, 0x69, 0x6e, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x05, 0x73, 0x74, 0x64, 0x69, 0x6e, 0x12, 0x13, 0x0a, 0x05,
	0x67, 0x6c, 0x5f, 0x69, 0x64, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x67, 0x6c, 0x49,
	0x64, 0x12, 0x23, 0x0a, 0x0d, 0x67, 0x6c, 0x5f, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f,
	0x72, 0x79, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0c, 0x67, 0x6c, 0x52, 0x65, 0x70, 0x6f,
	0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x12, 0x1f, 0x0a, 0x0b, 0x67, 0x6c, 0x5f, 0x75, 0x73, 0x65,
	0x72, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0a, 0x67, 0x6c, 0x55,
	0x73, 0x65, 0x72, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x21, 0x0a, 0x0c, 0x67, 0x69, 0x74, 0x5f, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x67,
	0x69, 0x74, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x12, 0x2c, 0x0a, 0x12, 0x67, 0x69,
	0x74, 0x5f, 0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x5f, 0x6f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73,
	0x18, 0x07, 0x20, 0x03, 0x28, 0x09, 0x52, 0x10, 0x67, 0x69, 0x74, 0x43, 0x6f, 0x6e, 0x66, 0x69,
	0x67, 0x4f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x22, 0x7d, 0x0a, 0x16, 0x53, 0x53, 0x48, 0x52,
	0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x74, 0x64, 0x6f, 0x75, 0x74, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0c, 0x52, 0x06, 0x73, 0x74, 0x64, 0x6f, 0x75, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x74,
	0x64, 0x65, 0x72, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x06, 0x73, 0x74, 0x64, 0x65,
	0x72, 0x72, 0x12, 0x33, 0x0a, 0x0b, 0x65, 0x78, 0x69, 0x74, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x75,
	0x73, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79,
	0x2e, 0x45, 0x78, 0x69, 0x74, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x52, 0x0a, 0x65, 0x78, 0x69,
	0x74, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x22, 0x69, 0x0a, 0x17, 0x53, 0x53, 0x48, 0x55, 0x70,
	0x6c, 0x6f, 0x61, 0x64, 0x41, 0x72, 0x63, 0x68, 0x69, 0x76, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e,
	0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01,
	0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x12, 0x14, 0x0a, 0x05,
	0x73, 0x74, 0x64, 0x69, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x05, 0x73, 0x74, 0x64,
	0x69, 0x6e, 0x22, 0x7f, 0x0a, 0x18, 0x53, 0x53, 0x48, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x41,
	0x72, 0x63, 0x68, 0x69, 0x76, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x16,
	0x0a, 0x06, 0x73, 0x74, 0x64, 0x6f, 0x75, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x06,
	0x73, 0x74, 0x64, 0x6f, 0x75, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x74, 0x64, 0x65, 0x72, 0x72,
	0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x06, 0x73, 0x74, 0x64, 0x65, 0x72, 0x72, 0x12, 0x33,
	0x0a, 0x0b, 0x65, 0x78, 0x69, 0x74, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x45, 0x78, 0x69,
	0x74, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x52, 0x0a, 0x65, 0x78, 0x69, 0x74, 0x53, 0x74, 0x61,
	0x74, 0x75, 0x73, 0x32, 0xa6, 0x02, 0x0a, 0x0a, 0x53, 0x53, 0x48, 0x53, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x12, 0x58, 0x0a, 0x0d, 0x53, 0x53, 0x48, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x50,
	0x61, 0x63, 0x6b, 0x12, 0x1c, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x53, 0x53, 0x48,
	0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x1d, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x53, 0x53, 0x48, 0x55, 0x70,
	0x6c, 0x6f, 0x61, 0x64, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x02, 0x28, 0x01, 0x30, 0x01, 0x12, 0x5b, 0x0a, 0x0e,
	0x53, 0x53, 0x48, 0x52, 0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x50, 0x61, 0x63, 0x6b, 0x12, 0x1d,
	0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x53, 0x53, 0x48, 0x52, 0x65, 0x63, 0x65, 0x69,
	0x76, 0x65, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1e, 0x2e,
	0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x53, 0x53, 0x48, 0x52, 0x65, 0x63, 0x65, 0x69, 0x76,
	0x65, 0x50, 0x61, 0x63, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa,
	0x97, 0x28, 0x02, 0x08, 0x01, 0x28, 0x01, 0x30, 0x01, 0x12, 0x61, 0x0a, 0x10, 0x53, 0x53, 0x48,
	0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64, 0x41, 0x72, 0x63, 0x68, 0x69, 0x76, 0x65, 0x12, 0x1f, 0x2e,
	0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x53, 0x53, 0x48, 0x55, 0x70, 0x6c, 0x6f, 0x61, 0x64,
	0x41, 0x72, 0x63, 0x68, 0x69, 0x76, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x20,
	0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x53, 0x53, 0x48, 0x55, 0x70, 0x6c, 0x6f, 0x61,
	0x64, 0x41, 0x72, 0x63, 0x68, 0x69, 0x76, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x02, 0x28, 0x01, 0x30, 0x01, 0x42, 0x34, 0x5a, 0x32,
	0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x67, 0x69, 0x74, 0x6c, 0x61,
	0x62, 0x2d, 0x6f, 0x72, 0x67, 0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2f, 0x76, 0x31, 0x34,
	0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x67, 0x6f, 0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79,
	0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_ssh_proto_rawDescOnce sync.Once
	file_ssh_proto_rawDescData = file_ssh_proto_rawDesc
)

func file_ssh_proto_rawDescGZIP() []byte {
	file_ssh_proto_rawDescOnce.Do(func() {
		file_ssh_proto_rawDescData = protoimpl.X.CompressGZIP(file_ssh_proto_rawDescData)
	})
	return file_ssh_proto_rawDescData
}

var file_ssh_proto_msgTypes = make([]protoimpl.MessageInfo, 6)
var file_ssh_proto_goTypes = []interface{}{
	(*SSHUploadPackRequest)(nil),     // 0: gitaly.SSHUploadPackRequest
	(*SSHUploadPackResponse)(nil),    // 1: gitaly.SSHUploadPackResponse
	(*SSHReceivePackRequest)(nil),    // 2: gitaly.SSHReceivePackRequest
	(*SSHReceivePackResponse)(nil),   // 3: gitaly.SSHReceivePackResponse
	(*SSHUploadArchiveRequest)(nil),  // 4: gitaly.SSHUploadArchiveRequest
	(*SSHUploadArchiveResponse)(nil), // 5: gitaly.SSHUploadArchiveResponse
	(*Repository)(nil),               // 6: gitaly.Repository
	(*ExitStatus)(nil),               // 7: gitaly.ExitStatus
}
var file_ssh_proto_depIdxs = []int32{
	6, // 0: gitaly.SSHUploadPackRequest.repository:type_name -> gitaly.Repository
	7, // 1: gitaly.SSHUploadPackResponse.exit_status:type_name -> gitaly.ExitStatus
	6, // 2: gitaly.SSHReceivePackRequest.repository:type_name -> gitaly.Repository
	7, // 3: gitaly.SSHReceivePackResponse.exit_status:type_name -> gitaly.ExitStatus
	6, // 4: gitaly.SSHUploadArchiveRequest.repository:type_name -> gitaly.Repository
	7, // 5: gitaly.SSHUploadArchiveResponse.exit_status:type_name -> gitaly.ExitStatus
	0, // 6: gitaly.SSHService.SSHUploadPack:input_type -> gitaly.SSHUploadPackRequest
	2, // 7: gitaly.SSHService.SSHReceivePack:input_type -> gitaly.SSHReceivePackRequest
	4, // 8: gitaly.SSHService.SSHUploadArchive:input_type -> gitaly.SSHUploadArchiveRequest
	1, // 9: gitaly.SSHService.SSHUploadPack:output_type -> gitaly.SSHUploadPackResponse
	3, // 10: gitaly.SSHService.SSHReceivePack:output_type -> gitaly.SSHReceivePackResponse
	5, // 11: gitaly.SSHService.SSHUploadArchive:output_type -> gitaly.SSHUploadArchiveResponse
	9, // [9:12] is the sub-list for method output_type
	6, // [6:9] is the sub-list for method input_type
	6, // [6:6] is the sub-list for extension type_name
	6, // [6:6] is the sub-list for extension extendee
	0, // [0:6] is the sub-list for field type_name
}

func init() { file_ssh_proto_init() }
func file_ssh_proto_init() {
	if File_ssh_proto != nil {
		return
	}
	file_lint_proto_init()
	file_shared_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_ssh_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SSHUploadPackRequest); i {
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
		file_ssh_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SSHUploadPackResponse); i {
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
		file_ssh_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SSHReceivePackRequest); i {
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
		file_ssh_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SSHReceivePackResponse); i {
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
		file_ssh_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SSHUploadArchiveRequest); i {
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
		file_ssh_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SSHUploadArchiveResponse); i {
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
			RawDescriptor: file_ssh_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   6,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_ssh_proto_goTypes,
		DependencyIndexes: file_ssh_proto_depIdxs,
		MessageInfos:      file_ssh_proto_msgTypes,
	}.Build()
	File_ssh_proto = out.File
	file_ssh_proto_rawDesc = nil
	file_ssh_proto_goTypes = nil
	file_ssh_proto_depIdxs = nil
}
