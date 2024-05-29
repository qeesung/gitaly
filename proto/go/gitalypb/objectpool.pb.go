// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.1
// 	protoc        v4.23.1
// source: objectpool.proto

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

// CreateObjectPoolRequest is a request for the CreateObjectPool RPC.
type CreateObjectPoolRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// object_pool is the object pool to create. This field controls where exactly the object pool will
	// be created.
	ObjectPool *ObjectPool `protobuf:"bytes,1,opt,name=object_pool,json=objectPool,proto3" json:"object_pool,omitempty"`
	// origin is the repository from which the object pool shall be created.
	Origin *Repository `protobuf:"bytes,2,opt,name=origin,proto3" json:"origin,omitempty"`
}

func (x *CreateObjectPoolRequest) Reset() {
	*x = CreateObjectPoolRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *CreateObjectPoolRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateObjectPoolRequest) ProtoMessage() {}

func (x *CreateObjectPoolRequest) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateObjectPoolRequest.ProtoReflect.Descriptor instead.
func (*CreateObjectPoolRequest) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{0}
}

func (x *CreateObjectPoolRequest) GetObjectPool() *ObjectPool {
	if x != nil {
		return x.ObjectPool
	}
	return nil
}

func (x *CreateObjectPoolRequest) GetOrigin() *Repository {
	if x != nil {
		return x.Origin
	}
	return nil
}

// CreateObjectPoolResponse is a response for the CreateObjectPool RPC.
type CreateObjectPoolResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *CreateObjectPoolResponse) Reset() {
	*x = CreateObjectPoolResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *CreateObjectPoolResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateObjectPoolResponse) ProtoMessage() {}

func (x *CreateObjectPoolResponse) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateObjectPoolResponse.ProtoReflect.Descriptor instead.
func (*CreateObjectPoolResponse) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{1}
}

// DeleteObjectPoolRequest is a request for the DeleteObjectPool RPC.
type DeleteObjectPoolRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// object_pool is the object pool that shall be deleted.
	ObjectPool *ObjectPool `protobuf:"bytes,1,opt,name=object_pool,json=objectPool,proto3" json:"object_pool,omitempty"`
}

func (x *DeleteObjectPoolRequest) Reset() {
	*x = DeleteObjectPoolRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DeleteObjectPoolRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteObjectPoolRequest) ProtoMessage() {}

func (x *DeleteObjectPoolRequest) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteObjectPoolRequest.ProtoReflect.Descriptor instead.
func (*DeleteObjectPoolRequest) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{2}
}

func (x *DeleteObjectPoolRequest) GetObjectPool() *ObjectPool {
	if x != nil {
		return x.ObjectPool
	}
	return nil
}

// DeleteObjectPoolResponse is a response for the DeleteObjectPool RPC.
type DeleteObjectPoolResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *DeleteObjectPoolResponse) Reset() {
	*x = DeleteObjectPoolResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DeleteObjectPoolResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteObjectPoolResponse) ProtoMessage() {}

func (x *DeleteObjectPoolResponse) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteObjectPoolResponse.ProtoReflect.Descriptor instead.
func (*DeleteObjectPoolResponse) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{3}
}

// LinkRepositoryToObjectPoolRequest is a request for the LinkRepositoryToObjectPool RPC.
type LinkRepositoryToObjectPoolRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// object_pool is the object pool to which the repository shall be linked to.
	ObjectPool *ObjectPool `protobuf:"bytes,1,opt,name=object_pool,json=objectPool,proto3" json:"object_pool,omitempty"`
	// repository is the repository that shall be linked to the object pool.
	Repository *Repository `protobuf:"bytes,2,opt,name=repository,proto3" json:"repository,omitempty"`
}

func (x *LinkRepositoryToObjectPoolRequest) Reset() {
	*x = LinkRepositoryToObjectPoolRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LinkRepositoryToObjectPoolRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LinkRepositoryToObjectPoolRequest) ProtoMessage() {}

func (x *LinkRepositoryToObjectPoolRequest) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LinkRepositoryToObjectPoolRequest.ProtoReflect.Descriptor instead.
func (*LinkRepositoryToObjectPoolRequest) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{4}
}

func (x *LinkRepositoryToObjectPoolRequest) GetObjectPool() *ObjectPool {
	if x != nil {
		return x.ObjectPool
	}
	return nil
}

func (x *LinkRepositoryToObjectPoolRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

// LinkRepositoryToObjectPoolResponse is a response for the LinkRepositoryToObjectPool RPC.
type LinkRepositoryToObjectPoolResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *LinkRepositoryToObjectPoolResponse) Reset() {
	*x = LinkRepositoryToObjectPoolResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LinkRepositoryToObjectPoolResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LinkRepositoryToObjectPoolResponse) ProtoMessage() {}

func (x *LinkRepositoryToObjectPoolResponse) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LinkRepositoryToObjectPoolResponse.ProtoReflect.Descriptor instead.
func (*LinkRepositoryToObjectPoolResponse) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{5}
}

// DisconnectGitAlternatesRequest is a request for the DisconnectGitAlternates RPC.
type DisconnectGitAlternatesRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// repository is th repository that shall be disconnected from its object pool.
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
}

func (x *DisconnectGitAlternatesRequest) Reset() {
	*x = DisconnectGitAlternatesRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DisconnectGitAlternatesRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DisconnectGitAlternatesRequest) ProtoMessage() {}

func (x *DisconnectGitAlternatesRequest) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DisconnectGitAlternatesRequest.ProtoReflect.Descriptor instead.
func (*DisconnectGitAlternatesRequest) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{6}
}

func (x *DisconnectGitAlternatesRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

// DisconnectGitAlternatesResponse is a response for the DisconnectGitAlternates RPC.
type DisconnectGitAlternatesResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *DisconnectGitAlternatesResponse) Reset() {
	*x = DisconnectGitAlternatesResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DisconnectGitAlternatesResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DisconnectGitAlternatesResponse) ProtoMessage() {}

func (x *DisconnectGitAlternatesResponse) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DisconnectGitAlternatesResponse.ProtoReflect.Descriptor instead.
func (*DisconnectGitAlternatesResponse) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{7}
}

// FetchIntoObjectPoolRequest is a request for the FetchIntoObjectPool RPC.
type FetchIntoObjectPoolRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// origin is the repository to fetch changes from.
	Origin *Repository `protobuf:"bytes,1,opt,name=origin,proto3" json:"origin,omitempty"`
	// object_pool is the repository to fetch changes into.
	ObjectPool *ObjectPool `protobuf:"bytes,2,opt,name=object_pool,json=objectPool,proto3" json:"object_pool,omitempty"`
}

func (x *FetchIntoObjectPoolRequest) Reset() {
	*x = FetchIntoObjectPoolRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[8]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *FetchIntoObjectPoolRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FetchIntoObjectPoolRequest) ProtoMessage() {}

func (x *FetchIntoObjectPoolRequest) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[8]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FetchIntoObjectPoolRequest.ProtoReflect.Descriptor instead.
func (*FetchIntoObjectPoolRequest) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{8}
}

func (x *FetchIntoObjectPoolRequest) GetOrigin() *Repository {
	if x != nil {
		return x.Origin
	}
	return nil
}

func (x *FetchIntoObjectPoolRequest) GetObjectPool() *ObjectPool {
	if x != nil {
		return x.ObjectPool
	}
	return nil
}

// FetchIntoObjectPoolResponse is a response for the FetchIntoObjectPool RPC.
type FetchIntoObjectPoolResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *FetchIntoObjectPoolResponse) Reset() {
	*x = FetchIntoObjectPoolResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[9]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *FetchIntoObjectPoolResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FetchIntoObjectPoolResponse) ProtoMessage() {}

func (x *FetchIntoObjectPoolResponse) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[9]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FetchIntoObjectPoolResponse.ProtoReflect.Descriptor instead.
func (*FetchIntoObjectPoolResponse) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{9}
}

// GetObjectPoolRequest is a request for the GetObjectPool RPC.
type GetObjectPoolRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// repository is the repository for which the object pool shall be retrieved.
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
}

func (x *GetObjectPoolRequest) Reset() {
	*x = GetObjectPoolRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[10]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetObjectPoolRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetObjectPoolRequest) ProtoMessage() {}

func (x *GetObjectPoolRequest) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[10]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetObjectPoolRequest.ProtoReflect.Descriptor instead.
func (*GetObjectPoolRequest) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{10}
}

func (x *GetObjectPoolRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

// GetObjectPoolResponse is a response for the GetObjectPool RPC.
type GetObjectPoolResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// object_pool is the object pool the repository is connected to. If the repository is not
	// connected to any object pool, then this field will be empty.
	ObjectPool *ObjectPool `protobuf:"bytes,1,opt,name=object_pool,json=objectPool,proto3" json:"object_pool,omitempty"`
}

func (x *GetObjectPoolResponse) Reset() {
	*x = GetObjectPoolResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_objectpool_proto_msgTypes[11]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetObjectPoolResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetObjectPoolResponse) ProtoMessage() {}

func (x *GetObjectPoolResponse) ProtoReflect() protoreflect.Message {
	mi := &file_objectpool_proto_msgTypes[11]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetObjectPoolResponse.ProtoReflect.Descriptor instead.
func (*GetObjectPoolResponse) Descriptor() ([]byte, []int) {
	return file_objectpool_proto_rawDescGZIP(), []int{11}
}

func (x *GetObjectPoolResponse) GetObjectPool() *ObjectPool {
	if x != nil {
		return x.ObjectPool
	}
	return nil
}

var File_objectpool_proto protoreflect.FileDescriptor

var file_objectpool_proto_rawDesc = []byte{
	0x0a, 0x10, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x70, 0x6f, 0x6f, 0x6c, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x12, 0x06, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x1a, 0x0a, 0x6c, 0x69, 0x6e, 0x74,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x0c, 0x73, 0x68, 0x61, 0x72, 0x65, 0x64, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x22, 0x86, 0x01, 0x0a, 0x17, 0x43, 0x72, 0x65, 0x61, 0x74, 0x65, 0x4f,
	0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x12, 0x39, 0x0a, 0x0b, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x5f, 0x70, 0x6f, 0x6f, 0x6c, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4f,
	0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52,
	0x0a, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x12, 0x30, 0x0a, 0x06, 0x6f,
	0x72, 0x69, 0x67, 0x69, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69,
	0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42,
	0x04, 0xa0, 0xc6, 0x2c, 0x01, 0x52, 0x06, 0x6f, 0x72, 0x69, 0x67, 0x69, 0x6e, 0x22, 0x1a, 0x0a,
	0x18, 0x43, 0x72, 0x65, 0x61, 0x74, 0x65, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f,
	0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x54, 0x0a, 0x17, 0x44, 0x65, 0x6c,
	0x65, 0x74, 0x65, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x12, 0x39, 0x0a, 0x0b, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x5f, 0x70,
	0x6f, 0x6f, 0x6c, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61,
	0x6c, 0x79, 0x2e, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x42, 0x04, 0x98,
	0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x22,
	0x1a, 0x0a, 0x18, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50,
	0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x98, 0x01, 0x0a, 0x21,
	0x4c, 0x69, 0x6e, 0x6b, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x54, 0x6f,
	0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x12, 0x39, 0x0a, 0x0b, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x5f, 0x70, 0x6f, 0x6f, 0x6c,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e,
	0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x42, 0x04, 0xa0, 0xc6, 0x2c, 0x01,
	0x52, 0x0a, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x12, 0x38, 0x0a, 0x0a,
	0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69,
	0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f,
	0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x22, 0x24, 0x0a, 0x22, 0x4c, 0x69, 0x6e, 0x6b, 0x52, 0x65,
	0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x54, 0x6f, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74,
	0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x5a, 0x0a, 0x1e,
	0x44, 0x69, 0x73, 0x63, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x47, 0x69, 0x74, 0x41, 0x6c, 0x74,
	0x65, 0x72, 0x6e, 0x61, 0x74, 0x65, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x38,
	0x0a, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f,
	0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65,
	0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x22, 0x21, 0x0a, 0x1f, 0x44, 0x69, 0x73, 0x63,
	0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x47, 0x69, 0x74, 0x41, 0x6c, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x74, 0x65, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x97, 0x01, 0x0a, 0x1a,
	0x46, 0x65, 0x74, 0x63, 0x68, 0x49, 0x6e, 0x74, 0x6f, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50,
	0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x30, 0x0a, 0x06, 0x6f, 0x72,
	0x69, 0x67, 0x69, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74,
	0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04,
	0xa0, 0xc6, 0x2c, 0x01, 0x52, 0x06, 0x6f, 0x72, 0x69, 0x67, 0x69, 0x6e, 0x12, 0x39, 0x0a, 0x0b,
	0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x5f, 0x70, 0x6f, 0x6f, 0x6c, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4f, 0x62, 0x6a, 0x65, 0x63,
	0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x6f, 0x62, 0x6a,
	0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x4a, 0x04, 0x08, 0x03, 0x10, 0x04, 0x52, 0x06, 0x72,
	0x65, 0x70, 0x61, 0x63, 0x6b, 0x22, 0x1d, 0x0a, 0x1b, 0x46, 0x65, 0x74, 0x63, 0x68, 0x49, 0x6e,
	0x74, 0x6f, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x22, 0x50, 0x0a, 0x14, 0x47, 0x65, 0x74, 0x4f, 0x62, 0x6a, 0x65, 0x63,
	0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a,
	0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69,
	0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f,
	0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x22, 0x4c, 0x0a, 0x15, 0x47, 0x65, 0x74, 0x4f, 0x62, 0x6a,
	0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12,
	0x33, 0x0a, 0x0b, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x5f, 0x70, 0x6f, 0x6f, 0x6c, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4f, 0x62,
	0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x0a, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74,
	0x50, 0x6f, 0x6f, 0x6c, 0x32, 0x80, 0x05, 0x0a, 0x11, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50,
	0x6f, 0x6f, 0x6c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x5d, 0x0a, 0x10, 0x43, 0x72,
	0x65, 0x61, 0x74, 0x65, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x12, 0x1f,
	0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x43, 0x72, 0x65, 0x61, 0x74, 0x65, 0x4f, 0x62,
	0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a,
	0x20, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x43, 0x72, 0x65, 0x61, 0x74, 0x65, 0x4f,
	0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x01, 0x12, 0x5d, 0x0a, 0x10, 0x44, 0x65, 0x6c,
	0x65, 0x74, 0x65, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x12, 0x1f, 0x2e,
	0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4f, 0x62, 0x6a,
	0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x20,
	0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4f, 0x62,
	0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x01, 0x12, 0x7b, 0x0a, 0x1a, 0x4c, 0x69, 0x6e, 0x6b,
	0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x54, 0x6f, 0x4f, 0x62, 0x6a, 0x65,
	0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x12, 0x29, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e,
	0x4c, 0x69, 0x6e, 0x6b, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x54, 0x6f,
	0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x2a, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4c, 0x69, 0x6e, 0x6b, 0x52,
	0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x54, 0x6f, 0x4f, 0x62, 0x6a, 0x65, 0x63,
	0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa,
	0x97, 0x28, 0x02, 0x08, 0x01, 0x12, 0x72, 0x0a, 0x17, 0x44, 0x69, 0x73, 0x63, 0x6f, 0x6e, 0x6e,
	0x65, 0x63, 0x74, 0x47, 0x69, 0x74, 0x41, 0x6c, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x74, 0x65, 0x73,
	0x12, 0x26, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x44, 0x69, 0x73, 0x63, 0x6f, 0x6e,
	0x6e, 0x65, 0x63, 0x74, 0x47, 0x69, 0x74, 0x41, 0x6c, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x74, 0x65,
	0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x27, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c,
	0x79, 0x2e, 0x44, 0x69, 0x73, 0x63, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x47, 0x69, 0x74, 0x41,
	0x6c, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x74, 0x65, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x01, 0x12, 0x66, 0x0a, 0x13, 0x46, 0x65, 0x74,
	0x63, 0x68, 0x49, 0x6e, 0x74, 0x6f, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c,
	0x12, 0x22, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x46, 0x65, 0x74, 0x63, 0x68, 0x49,
	0x6e, 0x74, 0x6f, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x1a, 0x23, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x46, 0x65,
	0x74, 0x63, 0x68, 0x49, 0x6e, 0x74, 0x6f, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f,
	0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08,
	0x01, 0x12, 0x54, 0x0a, 0x0d, 0x47, 0x65, 0x74, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f,
	0x6f, 0x6c, 0x12, 0x1c, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x47, 0x65, 0x74, 0x4f,
	0x62, 0x6a, 0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x1a, 0x1d, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x47, 0x65, 0x74, 0x4f, 0x62, 0x6a,
	0x65, 0x63, 0x74, 0x50, 0x6f, 0x6f, 0x6c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22,
	0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x02, 0x42, 0x34, 0x5a, 0x32, 0x67, 0x69, 0x74, 0x6c, 0x61,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2d, 0x6f, 0x72, 0x67,
	0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2f, 0x76, 0x31, 0x36, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2f, 0x67, 0x6f, 0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x70, 0x62, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_objectpool_proto_rawDescOnce sync.Once
	file_objectpool_proto_rawDescData = file_objectpool_proto_rawDesc
)

func file_objectpool_proto_rawDescGZIP() []byte {
	file_objectpool_proto_rawDescOnce.Do(func() {
		file_objectpool_proto_rawDescData = protoimpl.X.CompressGZIP(file_objectpool_proto_rawDescData)
	})
	return file_objectpool_proto_rawDescData
}

var file_objectpool_proto_msgTypes = make([]protoimpl.MessageInfo, 12)
var file_objectpool_proto_goTypes = []interface{}{
	(*CreateObjectPoolRequest)(nil),            // 0: gitaly.CreateObjectPoolRequest
	(*CreateObjectPoolResponse)(nil),           // 1: gitaly.CreateObjectPoolResponse
	(*DeleteObjectPoolRequest)(nil),            // 2: gitaly.DeleteObjectPoolRequest
	(*DeleteObjectPoolResponse)(nil),           // 3: gitaly.DeleteObjectPoolResponse
	(*LinkRepositoryToObjectPoolRequest)(nil),  // 4: gitaly.LinkRepositoryToObjectPoolRequest
	(*LinkRepositoryToObjectPoolResponse)(nil), // 5: gitaly.LinkRepositoryToObjectPoolResponse
	(*DisconnectGitAlternatesRequest)(nil),     // 6: gitaly.DisconnectGitAlternatesRequest
	(*DisconnectGitAlternatesResponse)(nil),    // 7: gitaly.DisconnectGitAlternatesResponse
	(*FetchIntoObjectPoolRequest)(nil),         // 8: gitaly.FetchIntoObjectPoolRequest
	(*FetchIntoObjectPoolResponse)(nil),        // 9: gitaly.FetchIntoObjectPoolResponse
	(*GetObjectPoolRequest)(nil),               // 10: gitaly.GetObjectPoolRequest
	(*GetObjectPoolResponse)(nil),              // 11: gitaly.GetObjectPoolResponse
	(*ObjectPool)(nil),                         // 12: gitaly.ObjectPool
	(*Repository)(nil),                         // 13: gitaly.Repository
}
var file_objectpool_proto_depIdxs = []int32{
	12, // 0: gitaly.CreateObjectPoolRequest.object_pool:type_name -> gitaly.ObjectPool
	13, // 1: gitaly.CreateObjectPoolRequest.origin:type_name -> gitaly.Repository
	12, // 2: gitaly.DeleteObjectPoolRequest.object_pool:type_name -> gitaly.ObjectPool
	12, // 3: gitaly.LinkRepositoryToObjectPoolRequest.object_pool:type_name -> gitaly.ObjectPool
	13, // 4: gitaly.LinkRepositoryToObjectPoolRequest.repository:type_name -> gitaly.Repository
	13, // 5: gitaly.DisconnectGitAlternatesRequest.repository:type_name -> gitaly.Repository
	13, // 6: gitaly.FetchIntoObjectPoolRequest.origin:type_name -> gitaly.Repository
	12, // 7: gitaly.FetchIntoObjectPoolRequest.object_pool:type_name -> gitaly.ObjectPool
	13, // 8: gitaly.GetObjectPoolRequest.repository:type_name -> gitaly.Repository
	12, // 9: gitaly.GetObjectPoolResponse.object_pool:type_name -> gitaly.ObjectPool
	0,  // 10: gitaly.ObjectPoolService.CreateObjectPool:input_type -> gitaly.CreateObjectPoolRequest
	2,  // 11: gitaly.ObjectPoolService.DeleteObjectPool:input_type -> gitaly.DeleteObjectPoolRequest
	4,  // 12: gitaly.ObjectPoolService.LinkRepositoryToObjectPool:input_type -> gitaly.LinkRepositoryToObjectPoolRequest
	6,  // 13: gitaly.ObjectPoolService.DisconnectGitAlternates:input_type -> gitaly.DisconnectGitAlternatesRequest
	8,  // 14: gitaly.ObjectPoolService.FetchIntoObjectPool:input_type -> gitaly.FetchIntoObjectPoolRequest
	10, // 15: gitaly.ObjectPoolService.GetObjectPool:input_type -> gitaly.GetObjectPoolRequest
	1,  // 16: gitaly.ObjectPoolService.CreateObjectPool:output_type -> gitaly.CreateObjectPoolResponse
	3,  // 17: gitaly.ObjectPoolService.DeleteObjectPool:output_type -> gitaly.DeleteObjectPoolResponse
	5,  // 18: gitaly.ObjectPoolService.LinkRepositoryToObjectPool:output_type -> gitaly.LinkRepositoryToObjectPoolResponse
	7,  // 19: gitaly.ObjectPoolService.DisconnectGitAlternates:output_type -> gitaly.DisconnectGitAlternatesResponse
	9,  // 20: gitaly.ObjectPoolService.FetchIntoObjectPool:output_type -> gitaly.FetchIntoObjectPoolResponse
	11, // 21: gitaly.ObjectPoolService.GetObjectPool:output_type -> gitaly.GetObjectPoolResponse
	16, // [16:22] is the sub-list for method output_type
	10, // [10:16] is the sub-list for method input_type
	10, // [10:10] is the sub-list for extension type_name
	10, // [10:10] is the sub-list for extension extendee
	0,  // [0:10] is the sub-list for field type_name
}

func init() { file_objectpool_proto_init() }
func file_objectpool_proto_init() {
	if File_objectpool_proto != nil {
		return
	}
	file_lint_proto_init()
	file_shared_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_objectpool_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*CreateObjectPoolRequest); i {
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
		file_objectpool_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*CreateObjectPoolResponse); i {
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
		file_objectpool_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DeleteObjectPoolRequest); i {
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
		file_objectpool_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DeleteObjectPoolResponse); i {
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
		file_objectpool_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LinkRepositoryToObjectPoolRequest); i {
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
		file_objectpool_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LinkRepositoryToObjectPoolResponse); i {
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
		file_objectpool_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DisconnectGitAlternatesRequest); i {
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
		file_objectpool_proto_msgTypes[7].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DisconnectGitAlternatesResponse); i {
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
		file_objectpool_proto_msgTypes[8].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*FetchIntoObjectPoolRequest); i {
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
		file_objectpool_proto_msgTypes[9].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*FetchIntoObjectPoolResponse); i {
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
		file_objectpool_proto_msgTypes[10].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GetObjectPoolRequest); i {
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
		file_objectpool_proto_msgTypes[11].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GetObjectPoolResponse); i {
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
			RawDescriptor: file_objectpool_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   12,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_objectpool_proto_goTypes,
		DependencyIndexes: file_objectpool_proto_depIdxs,
		MessageInfos:      file_objectpool_proto_msgTypes,
	}.Build()
	File_objectpool_proto = out.File
	file_objectpool_proto_rawDesc = nil
	file_objectpool_proto_goTypes = nil
	file_objectpool_proto_depIdxs = nil
}
