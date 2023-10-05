// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        v4.23.1
// source: log.proto

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

// LogEntry is a single entry in a repository's write-ahead log.
//
// Schema for :
// - `repository/<repository_id>/log/entry/<log_index>`.
type LogEntry struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// reference_updates contains the reference updates this log
	// entry records. The logged reference updates have already passed
	// through verification and are applied without any further checks.
	ReferenceUpdates []*LogEntry_ReferenceUpdate `protobuf:"bytes,1,rep,name=reference_updates,json=referenceUpdates,proto3" json:"reference_updates,omitempty"`
	// default_branch_update contains the information pertaining to updating
	// the default branch of the repo.
	DefaultBranchUpdate *LogEntry_DefaultBranchUpdate `protobuf:"bytes,2,opt,name=default_branch_update,json=defaultBranchUpdate,proto3" json:"default_branch_update,omitempty"`
	// custom_hooks_update contains the custom hooks to set in the repository.
	CustomHooksUpdate *LogEntry_CustomHooksUpdate `protobuf:"bytes,3,opt,name=custom_hooks_update,json=customHooksUpdate,proto3" json:"custom_hooks_update,omitempty"`
	// pack_prefix contains the prefix (`pack-<digest>`) of the pack and its index.
	// If pack_prefix is empty, the log entry has no associated pack.
	PackPrefix string `protobuf:"bytes,4,opt,name=pack_prefix,json=packPrefix,proto3" json:"pack_prefix,omitempty"`
	// repository_deletion, when set, indicates this log entry deletes the repository.
	RepositoryDeletion *LogEntry_RepositoryDeletion `protobuf:"bytes,5,opt,name=repository_deletion,json=repositoryDeletion,proto3" json:"repository_deletion,omitempty"`
}

func (x *LogEntry) Reset() {
	*x = LogEntry{}
	if protoimpl.UnsafeEnabled {
		mi := &file_log_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LogEntry) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogEntry) ProtoMessage() {}

func (x *LogEntry) ProtoReflect() protoreflect.Message {
	mi := &file_log_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogEntry.ProtoReflect.Descriptor instead.
func (*LogEntry) Descriptor() ([]byte, []int) {
	return file_log_proto_rawDescGZIP(), []int{0}
}

func (x *LogEntry) GetReferenceUpdates() []*LogEntry_ReferenceUpdate {
	if x != nil {
		return x.ReferenceUpdates
	}
	return nil
}

func (x *LogEntry) GetDefaultBranchUpdate() *LogEntry_DefaultBranchUpdate {
	if x != nil {
		return x.DefaultBranchUpdate
	}
	return nil
}

func (x *LogEntry) GetCustomHooksUpdate() *LogEntry_CustomHooksUpdate {
	if x != nil {
		return x.CustomHooksUpdate
	}
	return nil
}

func (x *LogEntry) GetPackPrefix() string {
	if x != nil {
		return x.PackPrefix
	}
	return ""
}

func (x *LogEntry) GetRepositoryDeletion() *LogEntry_RepositoryDeletion {
	if x != nil {
		return x.RepositoryDeletion
	}
	return nil
}

// LogIndex serializes a log index. It's used for storing a repository's
// applied log index in the database.
//
// Schema for:
// - `repository/<repository_id>/log/index/applied`
type LogIndex struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// log_index is an index pointing to a position in the log.
	LogIndex uint64 `protobuf:"varint,1,opt,name=log_index,json=logIndex,proto3" json:"log_index,omitempty"`
}

func (x *LogIndex) Reset() {
	*x = LogIndex{}
	if protoimpl.UnsafeEnabled {
		mi := &file_log_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LogIndex) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogIndex) ProtoMessage() {}

func (x *LogIndex) ProtoReflect() protoreflect.Message {
	mi := &file_log_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogIndex.ProtoReflect.Descriptor instead.
func (*LogIndex) Descriptor() ([]byte, []int) {
	return file_log_proto_rawDescGZIP(), []int{1}
}

func (x *LogIndex) GetLogIndex() uint64 {
	if x != nil {
		return x.LogIndex
	}
	return 0
}

// ReferenceUpdate models a single reference update.
type LogEntry_ReferenceUpdate struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// reference_name is the fully qualified name of the reference
	// to update.
	ReferenceName []byte `protobuf:"bytes,1,opt,name=reference_name,json=referenceName,proto3" json:"reference_name,omitempty"`
	// new_oid is the new oid to point the reference to. Deletions
	// are denoted as the SHA1 or SHA256 zero OID depending on the
	// hash type used in the repository.
	NewOid []byte `protobuf:"bytes,2,opt,name=new_oid,json=newOid,proto3" json:"new_oid,omitempty"`
}

func (x *LogEntry_ReferenceUpdate) Reset() {
	*x = LogEntry_ReferenceUpdate{}
	if protoimpl.UnsafeEnabled {
		mi := &file_log_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LogEntry_ReferenceUpdate) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogEntry_ReferenceUpdate) ProtoMessage() {}

func (x *LogEntry_ReferenceUpdate) ProtoReflect() protoreflect.Message {
	mi := &file_log_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogEntry_ReferenceUpdate.ProtoReflect.Descriptor instead.
func (*LogEntry_ReferenceUpdate) Descriptor() ([]byte, []int) {
	return file_log_proto_rawDescGZIP(), []int{0, 0}
}

func (x *LogEntry_ReferenceUpdate) GetReferenceName() []byte {
	if x != nil {
		return x.ReferenceName
	}
	return nil
}

func (x *LogEntry_ReferenceUpdate) GetNewOid() []byte {
	if x != nil {
		return x.NewOid
	}
	return nil
}

// DefaultBranchUpdate models a default branch update.
type LogEntry_DefaultBranchUpdate struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// reference_name is the fully qualified name of the reference
	// to update the default branch to.
	ReferenceName []byte `protobuf:"bytes,1,opt,name=reference_name,json=referenceName,proto3" json:"reference_name,omitempty"`
}

func (x *LogEntry_DefaultBranchUpdate) Reset() {
	*x = LogEntry_DefaultBranchUpdate{}
	if protoimpl.UnsafeEnabled {
		mi := &file_log_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LogEntry_DefaultBranchUpdate) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogEntry_DefaultBranchUpdate) ProtoMessage() {}

func (x *LogEntry_DefaultBranchUpdate) ProtoReflect() protoreflect.Message {
	mi := &file_log_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogEntry_DefaultBranchUpdate.ProtoReflect.Descriptor instead.
func (*LogEntry_DefaultBranchUpdate) Descriptor() ([]byte, []int) {
	return file_log_proto_rawDescGZIP(), []int{0, 1}
}

func (x *LogEntry_DefaultBranchUpdate) GetReferenceName() []byte {
	if x != nil {
		return x.ReferenceName
	}
	return nil
}

// CustomHooksUpdate models an update to the custom hooks.
type LogEntry_CustomHooksUpdate struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// custom_hooks_tar is a TAR that contains the custom hooks in
	// `custom_hooks` directory. The contents of the directory are
	// unpacked as the custom hooks.
	CustomHooksTar []byte `protobuf:"bytes,1,opt,name=custom_hooks_tar,json=customHooksTar,proto3" json:"custom_hooks_tar,omitempty"`
}

func (x *LogEntry_CustomHooksUpdate) Reset() {
	*x = LogEntry_CustomHooksUpdate{}
	if protoimpl.UnsafeEnabled {
		mi := &file_log_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LogEntry_CustomHooksUpdate) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogEntry_CustomHooksUpdate) ProtoMessage() {}

func (x *LogEntry_CustomHooksUpdate) ProtoReflect() protoreflect.Message {
	mi := &file_log_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogEntry_CustomHooksUpdate.ProtoReflect.Descriptor instead.
func (*LogEntry_CustomHooksUpdate) Descriptor() ([]byte, []int) {
	return file_log_proto_rawDescGZIP(), []int{0, 2}
}

func (x *LogEntry_CustomHooksUpdate) GetCustomHooksTar() []byte {
	if x != nil {
		return x.CustomHooksTar
	}
	return nil
}

// RepositoryDeletion models a repository deletion.
type LogEntry_RepositoryDeletion struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *LogEntry_RepositoryDeletion) Reset() {
	*x = LogEntry_RepositoryDeletion{}
	if protoimpl.UnsafeEnabled {
		mi := &file_log_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LogEntry_RepositoryDeletion) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LogEntry_RepositoryDeletion) ProtoMessage() {}

func (x *LogEntry_RepositoryDeletion) ProtoReflect() protoreflect.Message {
	mi := &file_log_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LogEntry_RepositoryDeletion.ProtoReflect.Descriptor instead.
func (*LogEntry_RepositoryDeletion) Descriptor() ([]byte, []int) {
	return file_log_proto_rawDescGZIP(), []int{0, 3}
}

var File_log_proto protoreflect.FileDescriptor

var file_log_proto_rawDesc = []byte{
	0x0a, 0x09, 0x6c, 0x6f, 0x67, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x06, 0x67, 0x69, 0x74,
	0x61, 0x6c, 0x79, 0x22, 0xe4, 0x04, 0x0a, 0x08, 0x4c, 0x6f, 0x67, 0x45, 0x6e, 0x74, 0x72, 0x79,
	0x12, 0x4d, 0x0a, 0x11, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x5f, 0x75, 0x70,
	0x64, 0x61, 0x74, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x20, 0x2e, 0x67, 0x69,
	0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4c, 0x6f, 0x67, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x2e, 0x52, 0x65,
	0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x52, 0x10, 0x72,
	0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x73, 0x12,
	0x58, 0x0a, 0x15, 0x64, 0x65, 0x66, 0x61, 0x75, 0x6c, 0x74, 0x5f, 0x62, 0x72, 0x61, 0x6e, 0x63,
	0x68, 0x5f, 0x75, 0x70, 0x64, 0x61, 0x74, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x24,
	0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4c, 0x6f, 0x67, 0x45, 0x6e, 0x74, 0x72, 0x79,
	0x2e, 0x44, 0x65, 0x66, 0x61, 0x75, 0x6c, 0x74, 0x42, 0x72, 0x61, 0x6e, 0x63, 0x68, 0x55, 0x70,
	0x64, 0x61, 0x74, 0x65, 0x52, 0x13, 0x64, 0x65, 0x66, 0x61, 0x75, 0x6c, 0x74, 0x42, 0x72, 0x61,
	0x6e, 0x63, 0x68, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x12, 0x52, 0x0a, 0x13, 0x63, 0x75, 0x73,
	0x74, 0x6f, 0x6d, 0x5f, 0x68, 0x6f, 0x6f, 0x6b, 0x73, 0x5f, 0x75, 0x70, 0x64, 0x61, 0x74, 0x65,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x22, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e,
	0x4c, 0x6f, 0x67, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x2e, 0x43, 0x75, 0x73, 0x74, 0x6f, 0x6d, 0x48,
	0x6f, 0x6f, 0x6b, 0x73, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x52, 0x11, 0x63, 0x75, 0x73, 0x74,
	0x6f, 0x6d, 0x48, 0x6f, 0x6f, 0x6b, 0x73, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x12, 0x1f, 0x0a,
	0x0b, 0x70, 0x61, 0x63, 0x6b, 0x5f, 0x70, 0x72, 0x65, 0x66, 0x69, 0x78, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0a, 0x70, 0x61, 0x63, 0x6b, 0x50, 0x72, 0x65, 0x66, 0x69, 0x78, 0x12, 0x54,
	0x0a, 0x13, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x5f, 0x64, 0x65, 0x6c,
	0x65, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x23, 0x2e, 0x67, 0x69,
	0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4c, 0x6f, 0x67, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x2e, 0x52, 0x65,
	0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x69, 0x6f, 0x6e,
	0x52, 0x12, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x44, 0x65, 0x6c, 0x65,
	0x74, 0x69, 0x6f, 0x6e, 0x1a, 0x51, 0x0a, 0x0f, 0x52, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63,
	0x65, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x12, 0x25, 0x0a, 0x0e, 0x72, 0x65, 0x66, 0x65, 0x72,
	0x65, 0x6e, 0x63, 0x65, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52,
	0x0d, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x17,
	0x0a, 0x07, 0x6e, 0x65, 0x77, 0x5f, 0x6f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52,
	0x06, 0x6e, 0x65, 0x77, 0x4f, 0x69, 0x64, 0x1a, 0x3c, 0x0a, 0x13, 0x44, 0x65, 0x66, 0x61, 0x75,
	0x6c, 0x74, 0x42, 0x72, 0x61, 0x6e, 0x63, 0x68, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x12, 0x25,
	0x0a, 0x0e, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x5f, 0x6e, 0x61, 0x6d, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x0d, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63,
	0x65, 0x4e, 0x61, 0x6d, 0x65, 0x1a, 0x3d, 0x0a, 0x11, 0x43, 0x75, 0x73, 0x74, 0x6f, 0x6d, 0x48,
	0x6f, 0x6f, 0x6b, 0x73, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x12, 0x28, 0x0a, 0x10, 0x63, 0x75,
	0x73, 0x74, 0x6f, 0x6d, 0x5f, 0x68, 0x6f, 0x6f, 0x6b, 0x73, 0x5f, 0x74, 0x61, 0x72, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0c, 0x52, 0x0e, 0x63, 0x75, 0x73, 0x74, 0x6f, 0x6d, 0x48, 0x6f, 0x6f, 0x6b,
	0x73, 0x54, 0x61, 0x72, 0x1a, 0x14, 0x0a, 0x12, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f,
	0x72, 0x79, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0x27, 0x0a, 0x08, 0x4c, 0x6f,
	0x67, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x12, 0x1b, 0x0a, 0x09, 0x6c, 0x6f, 0x67, 0x5f, 0x69, 0x6e,
	0x64, 0x65, 0x78, 0x18, 0x01, 0x20, 0x01, 0x28, 0x04, 0x52, 0x08, 0x6c, 0x6f, 0x67, 0x49, 0x6e,
	0x64, 0x65, 0x78, 0x42, 0x34, 0x5a, 0x32, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2e, 0x63, 0x6f,
	0x6d, 0x2f, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2d, 0x6f, 0x72, 0x67, 0x2f, 0x67, 0x69, 0x74,
	0x61, 0x6c, 0x79, 0x2f, 0x76, 0x31, 0x36, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x67, 0x6f,
	0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x33,
}

var (
	file_log_proto_rawDescOnce sync.Once
	file_log_proto_rawDescData = file_log_proto_rawDesc
)

func file_log_proto_rawDescGZIP() []byte {
	file_log_proto_rawDescOnce.Do(func() {
		file_log_proto_rawDescData = protoimpl.X.CompressGZIP(file_log_proto_rawDescData)
	})
	return file_log_proto_rawDescData
}

var file_log_proto_msgTypes = make([]protoimpl.MessageInfo, 6)
var file_log_proto_goTypes = []interface{}{
	(*LogEntry)(nil),                     // 0: gitaly.LogEntry
	(*LogIndex)(nil),                     // 1: gitaly.LogIndex
	(*LogEntry_ReferenceUpdate)(nil),     // 2: gitaly.LogEntry.ReferenceUpdate
	(*LogEntry_DefaultBranchUpdate)(nil), // 3: gitaly.LogEntry.DefaultBranchUpdate
	(*LogEntry_CustomHooksUpdate)(nil),   // 4: gitaly.LogEntry.CustomHooksUpdate
	(*LogEntry_RepositoryDeletion)(nil),  // 5: gitaly.LogEntry.RepositoryDeletion
}
var file_log_proto_depIdxs = []int32{
	2, // 0: gitaly.LogEntry.reference_updates:type_name -> gitaly.LogEntry.ReferenceUpdate
	3, // 1: gitaly.LogEntry.default_branch_update:type_name -> gitaly.LogEntry.DefaultBranchUpdate
	4, // 2: gitaly.LogEntry.custom_hooks_update:type_name -> gitaly.LogEntry.CustomHooksUpdate
	5, // 3: gitaly.LogEntry.repository_deletion:type_name -> gitaly.LogEntry.RepositoryDeletion
	4, // [4:4] is the sub-list for method output_type
	4, // [4:4] is the sub-list for method input_type
	4, // [4:4] is the sub-list for extension type_name
	4, // [4:4] is the sub-list for extension extendee
	0, // [0:4] is the sub-list for field type_name
}

func init() { file_log_proto_init() }
func file_log_proto_init() {
	if File_log_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_log_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LogEntry); i {
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
		file_log_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LogIndex); i {
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
		file_log_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LogEntry_ReferenceUpdate); i {
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
		file_log_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LogEntry_DefaultBranchUpdate); i {
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
		file_log_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LogEntry_CustomHooksUpdate); i {
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
		file_log_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LogEntry_RepositoryDeletion); i {
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
			RawDescriptor: file_log_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   6,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_log_proto_goTypes,
		DependencyIndexes: file_log_proto_depIdxs,
		MessageInfos:      file_log_proto_msgTypes,
	}.Build()
	File_log_proto = out.File
	file_log_proto_rawDesc = nil
	file_log_proto_goTypes = nil
	file_log_proto_depIdxs = nil
}
