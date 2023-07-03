// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        v4.23.1
// source: testproto/valid.proto

package testproto

import (
	gitalypb "gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
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

type ValidRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Destination *gitalypb.Repository `protobuf:"bytes,1,opt,name=destination,proto3" json:"destination,omitempty"`
}

func (x *ValidRequest) Reset() {
	*x = ValidRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidRequest) ProtoMessage() {}

func (x *ValidRequest) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidRequest.ProtoReflect.Descriptor instead.
func (*ValidRequest) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{0}
}

func (x *ValidRequest) GetDestination() *gitalypb.Repository {
	if x != nil {
		return x.Destination
	}
	return nil
}

type ValidRequestWithoutRepo struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *ValidRequestWithoutRepo) Reset() {
	*x = ValidRequestWithoutRepo{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidRequestWithoutRepo) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidRequestWithoutRepo) ProtoMessage() {}

func (x *ValidRequestWithoutRepo) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidRequestWithoutRepo.ProtoReflect.Descriptor instead.
func (*ValidRequestWithoutRepo) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{1}
}

type ValidStorageRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	StorageName string `protobuf:"bytes,1,opt,name=storage_name,json=storageName,proto3" json:"storage_name,omitempty"`
}

func (x *ValidStorageRequest) Reset() {
	*x = ValidStorageRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidStorageRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidStorageRequest) ProtoMessage() {}

func (x *ValidStorageRequest) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidStorageRequest.ProtoReflect.Descriptor instead.
func (*ValidStorageRequest) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{2}
}

func (x *ValidStorageRequest) GetStorageName() string {
	if x != nil {
		return x.StorageName
	}
	return ""
}

type ValidResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *ValidResponse) Reset() {
	*x = ValidResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidResponse) ProtoMessage() {}

func (x *ValidResponse) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidResponse.ProtoReflect.Descriptor instead.
func (*ValidResponse) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{3}
}

type ValidNestedRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	InnerMessage *ValidRequest `protobuf:"bytes,1,opt,name=inner_message,json=innerMessage,proto3" json:"inner_message,omitempty"`
}

func (x *ValidNestedRequest) Reset() {
	*x = ValidNestedRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidNestedRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidNestedRequest) ProtoMessage() {}

func (x *ValidNestedRequest) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidNestedRequest.ProtoReflect.Descriptor instead.
func (*ValidNestedRequest) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{4}
}

func (x *ValidNestedRequest) GetInnerMessage() *ValidRequest {
	if x != nil {
		return x.InnerMessage
	}
	return nil
}

type ValidStorageNestedRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	InnerMessage *ValidStorageRequest `protobuf:"bytes,1,opt,name=inner_message,json=innerMessage,proto3" json:"inner_message,omitempty"`
}

func (x *ValidStorageNestedRequest) Reset() {
	*x = ValidStorageNestedRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidStorageNestedRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidStorageNestedRequest) ProtoMessage() {}

func (x *ValidStorageNestedRequest) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidStorageNestedRequest.ProtoReflect.Descriptor instead.
func (*ValidStorageNestedRequest) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{5}
}

func (x *ValidStorageNestedRequest) GetInnerMessage() *ValidStorageRequest {
	if x != nil {
		return x.InnerMessage
	}
	return nil
}

type ValidNestedSharedRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	NestedTargetRepo *gitalypb.ObjectPool `protobuf:"bytes,1,opt,name=nested_target_repo,json=nestedTargetRepo,proto3" json:"nested_target_repo,omitempty"`
}

func (x *ValidNestedSharedRequest) Reset() {
	*x = ValidNestedSharedRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidNestedSharedRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidNestedSharedRequest) ProtoMessage() {}

func (x *ValidNestedSharedRequest) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidNestedSharedRequest.ProtoReflect.Descriptor instead.
func (*ValidNestedSharedRequest) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{6}
}

func (x *ValidNestedSharedRequest) GetNestedTargetRepo() *gitalypb.ObjectPool {
	if x != nil {
		return x.NestedTargetRepo
	}
	return nil
}

type ValidInnerNestedRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Header *ValidInnerNestedRequest_Header `protobuf:"bytes,1,opt,name=header,proto3" json:"header,omitempty"`
}

func (x *ValidInnerNestedRequest) Reset() {
	*x = ValidInnerNestedRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidInnerNestedRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidInnerNestedRequest) ProtoMessage() {}

func (x *ValidInnerNestedRequest) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidInnerNestedRequest.ProtoReflect.Descriptor instead.
func (*ValidInnerNestedRequest) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{7}
}

func (x *ValidInnerNestedRequest) GetHeader() *ValidInnerNestedRequest_Header {
	if x != nil {
		return x.Header
	}
	return nil
}

type ValidStorageInnerNestedRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Header *ValidStorageInnerNestedRequest_Header `protobuf:"bytes,1,opt,name=header,proto3" json:"header,omitempty"`
}

func (x *ValidStorageInnerNestedRequest) Reset() {
	*x = ValidStorageInnerNestedRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[8]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidStorageInnerNestedRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidStorageInnerNestedRequest) ProtoMessage() {}

func (x *ValidStorageInnerNestedRequest) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[8]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidStorageInnerNestedRequest.ProtoReflect.Descriptor instead.
func (*ValidStorageInnerNestedRequest) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{8}
}

func (x *ValidStorageInnerNestedRequest) GetHeader() *ValidStorageInnerNestedRequest_Header {
	if x != nil {
		return x.Header
	}
	return nil
}

type ValidInnerNestedRequest_Header struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Destination *gitalypb.Repository `protobuf:"bytes,1,opt,name=destination,proto3" json:"destination,omitempty"`
}

func (x *ValidInnerNestedRequest_Header) Reset() {
	*x = ValidInnerNestedRequest_Header{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[9]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidInnerNestedRequest_Header) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidInnerNestedRequest_Header) ProtoMessage() {}

func (x *ValidInnerNestedRequest_Header) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[9]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidInnerNestedRequest_Header.ProtoReflect.Descriptor instead.
func (*ValidInnerNestedRequest_Header) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{7, 0}
}

func (x *ValidInnerNestedRequest_Header) GetDestination() *gitalypb.Repository {
	if x != nil {
		return x.Destination
	}
	return nil
}

type ValidStorageInnerNestedRequest_Header struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	StorageName string `protobuf:"bytes,1,opt,name=storage_name,json=storageName,proto3" json:"storage_name,omitempty"`
}

func (x *ValidStorageInnerNestedRequest_Header) Reset() {
	*x = ValidStorageInnerNestedRequest_Header{}
	if protoimpl.UnsafeEnabled {
		mi := &file_testproto_valid_proto_msgTypes[10]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ValidStorageInnerNestedRequest_Header) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ValidStorageInnerNestedRequest_Header) ProtoMessage() {}

func (x *ValidStorageInnerNestedRequest_Header) ProtoReflect() protoreflect.Message {
	mi := &file_testproto_valid_proto_msgTypes[10]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ValidStorageInnerNestedRequest_Header.ProtoReflect.Descriptor instead.
func (*ValidStorageInnerNestedRequest_Header) Descriptor() ([]byte, []int) {
	return file_testproto_valid_proto_rawDescGZIP(), []int{8, 0}
}

func (x *ValidStorageInnerNestedRequest_Header) GetStorageName() string {
	if x != nil {
		return x.StorageName
	}
	return ""
}

var File_testproto_valid_proto protoreflect.FileDescriptor

var file_testproto_valid_proto_rawDesc = []byte{
	0x0a, 0x15, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x76, 0x61, 0x6c, 0x69,
	0x64, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x09, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x1a, 0x0a, 0x6c, 0x69, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x0c,
	0x73, 0x68, 0x61, 0x72, 0x65, 0x64, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x4a, 0x0a, 0x0c,
	0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x3a, 0x0a, 0x0b,
	0x64, 0x65, 0x73, 0x74, 0x69, 0x6e, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73,
	0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0b, 0x64, 0x65, 0x73,
	0x74, 0x69, 0x6e, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0x19, 0x0a, 0x17, 0x56, 0x61, 0x6c, 0x69,
	0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x57, 0x69, 0x74, 0x68, 0x6f, 0x75, 0x74, 0x52,
	0x65, 0x70, 0x6f, 0x22, 0x3e, 0x0a, 0x13, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x53, 0x74, 0x6f, 0x72,
	0x61, 0x67, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x27, 0x0a, 0x0c, 0x73, 0x74,
	0x6f, 0x72, 0x61, 0x67, 0x65, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x42, 0x04, 0x88, 0xc6, 0x2c, 0x01, 0x52, 0x0b, 0x73, 0x74, 0x6f, 0x72, 0x61, 0x67, 0x65, 0x4e,
	0x61, 0x6d, 0x65, 0x22, 0x0f, 0x0a, 0x0d, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x22, 0x52, 0x0a, 0x12, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x4e, 0x65, 0x73,
	0x74, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x3c, 0x0a, 0x0d, 0x69, 0x6e,
	0x6e, 0x65, 0x72, 0x5f, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x17, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61,
	0x6c, 0x69, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x52, 0x0c, 0x69, 0x6e, 0x6e, 0x65,
	0x72, 0x4d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x60, 0x0a, 0x19, 0x56, 0x61, 0x6c, 0x69,
	0x64, 0x53, 0x74, 0x6f, 0x72, 0x61, 0x67, 0x65, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x43, 0x0a, 0x0d, 0x69, 0x6e, 0x6e, 0x65, 0x72, 0x5f, 0x6d,
	0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1e, 0x2e, 0x74,
	0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x53, 0x74,
	0x6f, 0x72, 0x61, 0x67, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x52, 0x0c, 0x69, 0x6e,
	0x6e, 0x65, 0x72, 0x4d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x62, 0x0a, 0x18, 0x56, 0x61,
	0x6c, 0x69, 0x64, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x53, 0x68, 0x61, 0x72, 0x65, 0x64, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x46, 0x0a, 0x12, 0x6e, 0x65, 0x73, 0x74, 0x65, 0x64,
	0x5f, 0x74, 0x61, 0x72, 0x67, 0x65, 0x74, 0x5f, 0x72, 0x65, 0x70, 0x6f, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4f, 0x62, 0x6a, 0x65,
	0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x10, 0x6e, 0x65,
	0x73, 0x74, 0x65, 0x64, 0x54, 0x61, 0x72, 0x67, 0x65, 0x74, 0x52, 0x65, 0x70, 0x6f, 0x22, 0xa2,
	0x01, 0x0a, 0x17, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x49, 0x6e, 0x6e, 0x65, 0x72, 0x4e, 0x65, 0x73,
	0x74, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x41, 0x0a, 0x06, 0x68, 0x65,
	0x61, 0x64, 0x65, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x29, 0x2e, 0x74, 0x65, 0x73,
	0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x49, 0x6e, 0x6e, 0x65,
	0x72, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x2e, 0x48,
	0x65, 0x61, 0x64, 0x65, 0x72, 0x52, 0x06, 0x68, 0x65, 0x61, 0x64, 0x65, 0x72, 0x1a, 0x44, 0x0a,
	0x06, 0x48, 0x65, 0x61, 0x64, 0x65, 0x72, 0x12, 0x3a, 0x0a, 0x0b, 0x64, 0x65, 0x73, 0x74, 0x69,
	0x6e, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67,
	0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79,
	0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x74, 0x69, 0x6e, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x22, 0x9d, 0x01, 0x0a, 0x1e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x53, 0x74, 0x6f,
	0x72, 0x61, 0x67, 0x65, 0x49, 0x6e, 0x6e, 0x65, 0x72, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x48, 0x0a, 0x06, 0x68, 0x65, 0x61, 0x64, 0x65, 0x72,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x30, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x53, 0x74, 0x6f, 0x72, 0x61, 0x67, 0x65, 0x49,
	0x6e, 0x6e, 0x65, 0x72, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x2e, 0x48, 0x65, 0x61, 0x64, 0x65, 0x72, 0x52, 0x06, 0x68, 0x65, 0x61, 0x64, 0x65, 0x72,
	0x1a, 0x31, 0x0a, 0x06, 0x48, 0x65, 0x61, 0x64, 0x65, 0x72, 0x12, 0x27, 0x0a, 0x0c, 0x73, 0x74,
	0x6f, 0x72, 0x61, 0x67, 0x65, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x42, 0x04, 0x88, 0xc6, 0x2c, 0x01, 0x52, 0x0b, 0x73, 0x74, 0x6f, 0x72, 0x61, 0x67, 0x65, 0x4e,
	0x61, 0x6d, 0x65, 0x32, 0x5b, 0x0a, 0x12, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x63, 0x65, 0x70, 0x74,
	0x65, 0x64, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x3f, 0x0a, 0x0a, 0x54, 0x65, 0x73,
	0x74, 0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x12, 0x17, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c,
	0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x1a, 0x04, 0xf0, 0x97, 0x28, 0x01,
	0x32, 0xd0, 0x09, 0x0a, 0x0c, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63,
	0x65, 0x12, 0x47, 0x0a, 0x0a, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x12,
	0x17, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69,
	0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x02, 0x12, 0x48, 0x0a, 0x0b, 0x54, 0x65,
	0x73, 0x74, 0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x32, 0x12, 0x17, 0x2e, 0x74, 0x65, 0x73, 0x74,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56,
	0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97,
	0x28, 0x02, 0x08, 0x01, 0x12, 0x48, 0x0a, 0x0b, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x65, 0x74, 0x68,
	0x6f, 0x64, 0x33, 0x12, 0x17, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e,
	0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74,
	0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65,
	0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x01, 0x12, 0x4e,
	0x0a, 0x0b, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x35, 0x12, 0x1d, 0x2e,
	0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x4e,
	0x65, 0x73, 0x74, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74,
	0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65,
	0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x01, 0x12, 0x54,
	0x0a, 0x0b, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x36, 0x12, 0x23, 0x2e,
	0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x4e,
	0x65, 0x73, 0x74, 0x65, 0x64, 0x53, 0x68, 0x61, 0x72, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56,
	0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97,
	0x28, 0x02, 0x08, 0x01, 0x12, 0x53, 0x0a, 0x0b, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x65, 0x74, 0x68,
	0x6f, 0x64, 0x37, 0x12, 0x22, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e,
	0x56, 0x61, 0x6c, 0x69, 0x64, 0x49, 0x6e, 0x6e, 0x65, 0x72, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x01, 0x12, 0x51, 0x0a, 0x0b, 0x54, 0x65, 0x73,
	0x74, 0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x38, 0x12, 0x1e, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x53, 0x74, 0x6f, 0x72, 0x61, 0x67,
	0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x08, 0xfa, 0x97, 0x28, 0x04, 0x08, 0x01, 0x10, 0x02, 0x12, 0x57, 0x0a, 0x0b,
	0x54, 0x65, 0x73, 0x74, 0x4d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x39, 0x12, 0x24, 0x2e, 0x74, 0x65,
	0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x53, 0x74, 0x6f,
	0x72, 0x61, 0x67, 0x65, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61,
	0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x08, 0xfa, 0x97, 0x28,
	0x04, 0x08, 0x01, 0x10, 0x02, 0x12, 0x4e, 0x0a, 0x0c, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x65, 0x74,
	0x68, 0x6f, 0x64, 0x31, 0x30, 0x12, 0x1e, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x53, 0x74, 0x6f, 0x72, 0x61, 0x67, 0x65, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22,
	0x04, 0x80, 0x98, 0x28, 0x01, 0x12, 0x4c, 0x0a, 0x0f, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x61, 0x69,
	0x6e, 0x74, 0x65, 0x6e, 0x61, 0x6e, 0x63, 0x65, 0x12, 0x17, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61,
	0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28,
	0x02, 0x08, 0x03, 0x12, 0x5d, 0x0a, 0x20, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x61, 0x69, 0x6e, 0x74,
	0x65, 0x6e, 0x61, 0x6e, 0x63, 0x65, 0x57, 0x69, 0x74, 0x68, 0x45, 0x78, 0x70, 0x6c, 0x69, 0x63,
	0x69, 0x74, 0x53, 0x63, 0x6f, 0x70, 0x65, 0x12, 0x17, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c,
	0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02,
	0x08, 0x03, 0x12, 0x63, 0x0a, 0x20, 0x54, 0x65, 0x73, 0x74, 0x4d, 0x61, 0x69, 0x6e, 0x74, 0x65,
	0x6e, 0x61, 0x6e, 0x63, 0x65, 0x57, 0x69, 0x74, 0x68, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x1d, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22,
	0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x03, 0x12, 0x6f, 0x0a, 0x26, 0x54, 0x65, 0x73, 0x74, 0x4d,
	0x61, 0x69, 0x6e, 0x74, 0x65, 0x6e, 0x61, 0x6e, 0x63, 0x65, 0x57, 0x69, 0x74, 0x68, 0x4e, 0x65,
	0x73, 0x74, 0x65, 0x64, 0x53, 0x68, 0x61, 0x72, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x12, 0x23, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61,
	0x6c, 0x69, 0x64, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x53, 0x68, 0x61, 0x72, 0x65, 0x64, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x03, 0x12, 0x69, 0x0a, 0x21, 0x54, 0x65, 0x73, 0x74,
	0x4d, 0x75, 0x74, 0x61, 0x74, 0x6f, 0x72, 0x57, 0x69, 0x74, 0x68, 0x49, 0x6e, 0x6e, 0x65, 0x72,
	0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x22, 0x2e,
	0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61, 0x6c, 0x69, 0x64, 0x49,
	0x6e, 0x6e, 0x65, 0x72, 0x4e, 0x65, 0x73, 0x74, 0x65, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x18, 0x2e, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x56, 0x61,
	0x6c, 0x69, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28,
	0x02, 0x08, 0x03, 0x42, 0x3e, 0x5a, 0x3c, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2e, 0x63, 0x6f,
	0x6d, 0x2f, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2d, 0x6f, 0x72, 0x67, 0x2f, 0x67, 0x69, 0x74,
	0x61, 0x6c, 0x79, 0x2f, 0x76, 0x31, 0x36, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x67, 0x6f,
	0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x70, 0x62, 0x2f, 0x74, 0x65, 0x73, 0x74, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_testproto_valid_proto_rawDescOnce sync.Once
	file_testproto_valid_proto_rawDescData = file_testproto_valid_proto_rawDesc
)

func file_testproto_valid_proto_rawDescGZIP() []byte {
	file_testproto_valid_proto_rawDescOnce.Do(func() {
		file_testproto_valid_proto_rawDescData = protoimpl.X.CompressGZIP(file_testproto_valid_proto_rawDescData)
	})
	return file_testproto_valid_proto_rawDescData
}

var file_testproto_valid_proto_msgTypes = make([]protoimpl.MessageInfo, 11)
var file_testproto_valid_proto_goTypes = []interface{}{
	(*ValidRequest)(nil),                          // 0: testproto.ValidRequest
	(*ValidRequestWithoutRepo)(nil),               // 1: testproto.ValidRequestWithoutRepo
	(*ValidStorageRequest)(nil),                   // 2: testproto.ValidStorageRequest
	(*ValidResponse)(nil),                         // 3: testproto.ValidResponse
	(*ValidNestedRequest)(nil),                    // 4: testproto.ValidNestedRequest
	(*ValidStorageNestedRequest)(nil),             // 5: testproto.ValidStorageNestedRequest
	(*ValidNestedSharedRequest)(nil),              // 6: testproto.ValidNestedSharedRequest
	(*ValidInnerNestedRequest)(nil),               // 7: testproto.ValidInnerNestedRequest
	(*ValidStorageInnerNestedRequest)(nil),        // 8: testproto.ValidStorageInnerNestedRequest
	(*ValidInnerNestedRequest_Header)(nil),        // 9: testproto.ValidInnerNestedRequest.Header
	(*ValidStorageInnerNestedRequest_Header)(nil), // 10: testproto.ValidStorageInnerNestedRequest.Header
	(*gitalypb.Repository)(nil),                   // 11: gitaly.Repository
	(*gitalypb.ObjectPool)(nil),                   // 12: gitaly.ObjectPool
}
var file_testproto_valid_proto_depIdxs = []int32{
	11, // 0: testproto.ValidRequest.destination:type_name -> gitaly.Repository
	0,  // 1: testproto.ValidNestedRequest.inner_message:type_name -> testproto.ValidRequest
	2,  // 2: testproto.ValidStorageNestedRequest.inner_message:type_name -> testproto.ValidStorageRequest
	12, // 3: testproto.ValidNestedSharedRequest.nested_target_repo:type_name -> gitaly.ObjectPool
	9,  // 4: testproto.ValidInnerNestedRequest.header:type_name -> testproto.ValidInnerNestedRequest.Header
	10, // 5: testproto.ValidStorageInnerNestedRequest.header:type_name -> testproto.ValidStorageInnerNestedRequest.Header
	11, // 6: testproto.ValidInnerNestedRequest.Header.destination:type_name -> gitaly.Repository
	0,  // 7: testproto.InterceptedService.TestMethod:input_type -> testproto.ValidRequest
	0,  // 8: testproto.ValidService.TestMethod:input_type -> testproto.ValidRequest
	0,  // 9: testproto.ValidService.TestMethod2:input_type -> testproto.ValidRequest
	0,  // 10: testproto.ValidService.TestMethod3:input_type -> testproto.ValidRequest
	4,  // 11: testproto.ValidService.TestMethod5:input_type -> testproto.ValidNestedRequest
	6,  // 12: testproto.ValidService.TestMethod6:input_type -> testproto.ValidNestedSharedRequest
	7,  // 13: testproto.ValidService.TestMethod7:input_type -> testproto.ValidInnerNestedRequest
	2,  // 14: testproto.ValidService.TestMethod8:input_type -> testproto.ValidStorageRequest
	5,  // 15: testproto.ValidService.TestMethod9:input_type -> testproto.ValidStorageNestedRequest
	2,  // 16: testproto.ValidService.TestMethod10:input_type -> testproto.ValidStorageRequest
	0,  // 17: testproto.ValidService.TestMaintenance:input_type -> testproto.ValidRequest
	0,  // 18: testproto.ValidService.TestMaintenanceWithExplicitScope:input_type -> testproto.ValidRequest
	4,  // 19: testproto.ValidService.TestMaintenanceWithNestedRequest:input_type -> testproto.ValidNestedRequest
	6,  // 20: testproto.ValidService.TestMaintenanceWithNestedSharedRequest:input_type -> testproto.ValidNestedSharedRequest
	7,  // 21: testproto.ValidService.TestMutatorWithInnerNestedRequest:input_type -> testproto.ValidInnerNestedRequest
	3,  // 22: testproto.InterceptedService.TestMethod:output_type -> testproto.ValidResponse
	3,  // 23: testproto.ValidService.TestMethod:output_type -> testproto.ValidResponse
	3,  // 24: testproto.ValidService.TestMethod2:output_type -> testproto.ValidResponse
	3,  // 25: testproto.ValidService.TestMethod3:output_type -> testproto.ValidResponse
	3,  // 26: testproto.ValidService.TestMethod5:output_type -> testproto.ValidResponse
	3,  // 27: testproto.ValidService.TestMethod6:output_type -> testproto.ValidResponse
	3,  // 28: testproto.ValidService.TestMethod7:output_type -> testproto.ValidResponse
	3,  // 29: testproto.ValidService.TestMethod8:output_type -> testproto.ValidResponse
	3,  // 30: testproto.ValidService.TestMethod9:output_type -> testproto.ValidResponse
	3,  // 31: testproto.ValidService.TestMethod10:output_type -> testproto.ValidResponse
	3,  // 32: testproto.ValidService.TestMaintenance:output_type -> testproto.ValidResponse
	3,  // 33: testproto.ValidService.TestMaintenanceWithExplicitScope:output_type -> testproto.ValidResponse
	3,  // 34: testproto.ValidService.TestMaintenanceWithNestedRequest:output_type -> testproto.ValidResponse
	3,  // 35: testproto.ValidService.TestMaintenanceWithNestedSharedRequest:output_type -> testproto.ValidResponse
	3,  // 36: testproto.ValidService.TestMutatorWithInnerNestedRequest:output_type -> testproto.ValidResponse
	22, // [22:37] is the sub-list for method output_type
	7,  // [7:22] is the sub-list for method input_type
	7,  // [7:7] is the sub-list for extension type_name
	7,  // [7:7] is the sub-list for extension extendee
	0,  // [0:7] is the sub-list for field type_name
}

func init() { file_testproto_valid_proto_init() }
func file_testproto_valid_proto_init() {
	if File_testproto_valid_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_testproto_valid_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidRequest); i {
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
		file_testproto_valid_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidRequestWithoutRepo); i {
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
		file_testproto_valid_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidStorageRequest); i {
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
		file_testproto_valid_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidResponse); i {
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
		file_testproto_valid_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidNestedRequest); i {
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
		file_testproto_valid_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidStorageNestedRequest); i {
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
		file_testproto_valid_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidNestedSharedRequest); i {
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
		file_testproto_valid_proto_msgTypes[7].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidInnerNestedRequest); i {
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
		file_testproto_valid_proto_msgTypes[8].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidStorageInnerNestedRequest); i {
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
		file_testproto_valid_proto_msgTypes[9].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidInnerNestedRequest_Header); i {
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
		file_testproto_valid_proto_msgTypes[10].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ValidStorageInnerNestedRequest_Header); i {
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
			RawDescriptor: file_testproto_valid_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   11,
			NumExtensions: 0,
			NumServices:   2,
		},
		GoTypes:           file_testproto_valid_proto_goTypes,
		DependencyIndexes: file_testproto_valid_proto_depIdxs,
		MessageInfos:      file_testproto_valid_proto_msgTypes,
	}.Build()
	File_testproto_valid_proto = out.File
	file_testproto_valid_proto_rawDesc = nil
	file_testproto_valid_proto_goTypes = nil
	file_testproto_valid_proto_depIdxs = nil
}
