// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.26.0
// 	protoc        v3.17.3
// source: errors.proto

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

// AccessCheckError is an error returned by GitLab's `/internal/allowed`
// endpoint.
type AccessCheckError struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// ErrorMessage is the error message as returned by the endpoint.
	ErrorMessage string `protobuf:"bytes,1,opt,name=error_message,json=errorMessage,proto3" json:"error_message,omitempty"`
	// Protocol is the protocol used.
	Protocol string `protobuf:"bytes,2,opt,name=protocol,proto3" json:"protocol,omitempty"`
	// UserId is the user ID as which changes had been pushed.
	UserId string `protobuf:"bytes,3,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	// Changes is the set of changes which have failed the access check.
	Changes []byte `protobuf:"bytes,4,opt,name=changes,proto3" json:"changes,omitempty"`
}

func (x *AccessCheckError) Reset() {
	*x = AccessCheckError{}
	if protoimpl.UnsafeEnabled {
		mi := &file_errors_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AccessCheckError) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AccessCheckError) ProtoMessage() {}

func (x *AccessCheckError) ProtoReflect() protoreflect.Message {
	mi := &file_errors_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AccessCheckError.ProtoReflect.Descriptor instead.
func (*AccessCheckError) Descriptor() ([]byte, []int) {
	return file_errors_proto_rawDescGZIP(), []int{0}
}

func (x *AccessCheckError) GetErrorMessage() string {
	if x != nil {
		return x.ErrorMessage
	}
	return ""
}

func (x *AccessCheckError) GetProtocol() string {
	if x != nil {
		return x.Protocol
	}
	return ""
}

func (x *AccessCheckError) GetUserId() string {
	if x != nil {
		return x.UserId
	}
	return ""
}

func (x *AccessCheckError) GetChanges() []byte {
	if x != nil {
		return x.Changes
	}
	return nil
}

// MergeConflictError is an error returned in the case when merging two commits
// fails due to a merge conflict.
type MergeConflictError struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// ConflictingFiles is the set of files which have been conflicting. If this
	// field is empty, then there has still been a merge conflict, but it wasn't
	// able to determine which files have been conflicting.
	ConflictingFiles [][]byte `protobuf:"bytes,1,rep,name=conflicting_files,json=conflictingFiles,proto3" json:"conflicting_files,omitempty"`
}

func (x *MergeConflictError) Reset() {
	*x = MergeConflictError{}
	if protoimpl.UnsafeEnabled {
		mi := &file_errors_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *MergeConflictError) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MergeConflictError) ProtoMessage() {}

func (x *MergeConflictError) ProtoReflect() protoreflect.Message {
	mi := &file_errors_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MergeConflictError.ProtoReflect.Descriptor instead.
func (*MergeConflictError) Descriptor() ([]byte, []int) {
	return file_errors_proto_rawDescGZIP(), []int{1}
}

func (x *MergeConflictError) GetConflictingFiles() [][]byte {
	if x != nil {
		return x.ConflictingFiles
	}
	return nil
}

// ReferenceUpdateError is an error returned when updating a reference has
// failed.
type ReferenceUpdateError struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// ReferenceName is the name of the reference that failed to be updated.
	ReferenceName []byte `protobuf:"bytes,1,opt,name=reference_name,json=referenceName,proto3" json:"reference_name,omitempty"`
	// OldOid is the object ID the reference should have pointed to before the update.
	OldOid string `protobuf:"bytes,2,opt,name=old_oid,json=oldOid,proto3" json:"old_oid,omitempty"`
	// NewOid is the object ID the reference should have pointed to after the update.
	NewOid string `protobuf:"bytes,3,opt,name=new_oid,json=newOid,proto3" json:"new_oid,omitempty"`
}

func (x *ReferenceUpdateError) Reset() {
	*x = ReferenceUpdateError{}
	if protoimpl.UnsafeEnabled {
		mi := &file_errors_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ReferenceUpdateError) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReferenceUpdateError) ProtoMessage() {}

func (x *ReferenceUpdateError) ProtoReflect() protoreflect.Message {
	mi := &file_errors_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReferenceUpdateError.ProtoReflect.Descriptor instead.
func (*ReferenceUpdateError) Descriptor() ([]byte, []int) {
	return file_errors_proto_rawDescGZIP(), []int{2}
}

func (x *ReferenceUpdateError) GetReferenceName() []byte {
	if x != nil {
		return x.ReferenceName
	}
	return nil
}

func (x *ReferenceUpdateError) GetOldOid() string {
	if x != nil {
		return x.OldOid
	}
	return ""
}

func (x *ReferenceUpdateError) GetNewOid() string {
	if x != nil {
		return x.NewOid
	}
	return ""
}

// ResolveRevisionError is an error returned when resolving a specific revision
// has failed.
type ResolveRevisionError struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Revision is the name of the revision that was tried to be resolved.
	Revision []byte `protobuf:"bytes,1,opt,name=revision,proto3" json:"revision,omitempty"`
}

func (x *ResolveRevisionError) Reset() {
	*x = ResolveRevisionError{}
	if protoimpl.UnsafeEnabled {
		mi := &file_errors_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ResolveRevisionError) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ResolveRevisionError) ProtoMessage() {}

func (x *ResolveRevisionError) ProtoReflect() protoreflect.Message {
	mi := &file_errors_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ResolveRevisionError.ProtoReflect.Descriptor instead.
func (*ResolveRevisionError) Descriptor() ([]byte, []int) {
	return file_errors_proto_rawDescGZIP(), []int{3}
}

func (x *ResolveRevisionError) GetRevision() []byte {
	if x != nil {
		return x.Revision
	}
	return nil
}

var File_errors_proto protoreflect.FileDescriptor

var file_errors_proto_rawDesc = []byte{
	0x0a, 0x0c, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x06,
	0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x22, 0x86, 0x01, 0x0a, 0x10, 0x41, 0x63, 0x63, 0x65, 0x73,
	0x73, 0x43, 0x68, 0x65, 0x63, 0x6b, 0x45, 0x72, 0x72, 0x6f, 0x72, 0x12, 0x23, 0x0a, 0x0d, 0x65,
	0x72, 0x72, 0x6f, 0x72, 0x5f, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0c, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x4d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65,
	0x12, 0x1a, 0x0a, 0x08, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x08, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x12, 0x17, 0x0a, 0x07,
	0x75, 0x73, 0x65, 0x72, 0x5f, 0x69, 0x64, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x75,
	0x73, 0x65, 0x72, 0x49, 0x64, 0x12, 0x18, 0x0a, 0x07, 0x63, 0x68, 0x61, 0x6e, 0x67, 0x65, 0x73,
	0x18, 0x04, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x07, 0x63, 0x68, 0x61, 0x6e, 0x67, 0x65, 0x73, 0x22,
	0x41, 0x0a, 0x12, 0x4d, 0x65, 0x72, 0x67, 0x65, 0x43, 0x6f, 0x6e, 0x66, 0x6c, 0x69, 0x63, 0x74,
	0x45, 0x72, 0x72, 0x6f, 0x72, 0x12, 0x2b, 0x0a, 0x11, 0x63, 0x6f, 0x6e, 0x66, 0x6c, 0x69, 0x63,
	0x74, 0x69, 0x6e, 0x67, 0x5f, 0x66, 0x69, 0x6c, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0c,
	0x52, 0x10, 0x63, 0x6f, 0x6e, 0x66, 0x6c, 0x69, 0x63, 0x74, 0x69, 0x6e, 0x67, 0x46, 0x69, 0x6c,
	0x65, 0x73, 0x22, 0x6f, 0x0a, 0x14, 0x52, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x55,
	0x70, 0x64, 0x61, 0x74, 0x65, 0x45, 0x72, 0x72, 0x6f, 0x72, 0x12, 0x25, 0x0a, 0x0e, 0x72, 0x65,
	0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0c, 0x52, 0x0d, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x4e, 0x61, 0x6d,
	0x65, 0x12, 0x17, 0x0a, 0x07, 0x6f, 0x6c, 0x64, 0x5f, 0x6f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x06, 0x6f, 0x6c, 0x64, 0x4f, 0x69, 0x64, 0x12, 0x17, 0x0a, 0x07, 0x6e, 0x65,
	0x77, 0x5f, 0x6f, 0x69, 0x64, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6e, 0x65, 0x77,
	0x4f, 0x69, 0x64, 0x22, 0x32, 0x0a, 0x14, 0x52, 0x65, 0x73, 0x6f, 0x6c, 0x76, 0x65, 0x52, 0x65,
	0x76, 0x69, 0x73, 0x69, 0x6f, 0x6e, 0x45, 0x72, 0x72, 0x6f, 0x72, 0x12, 0x1a, 0x0a, 0x08, 0x72,
	0x65, 0x76, 0x69, 0x73, 0x69, 0x6f, 0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x08, 0x72,
	0x65, 0x76, 0x69, 0x73, 0x69, 0x6f, 0x6e, 0x42, 0x34, 0x5a, 0x32, 0x67, 0x69, 0x74, 0x6c, 0x61,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2d, 0x6f, 0x72, 0x67,
	0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2f, 0x76, 0x31, 0x34, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2f, 0x67, 0x6f, 0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x70, 0x62, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_errors_proto_rawDescOnce sync.Once
	file_errors_proto_rawDescData = file_errors_proto_rawDesc
)

func file_errors_proto_rawDescGZIP() []byte {
	file_errors_proto_rawDescOnce.Do(func() {
		file_errors_proto_rawDescData = protoimpl.X.CompressGZIP(file_errors_proto_rawDescData)
	})
	return file_errors_proto_rawDescData
}

var file_errors_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_errors_proto_goTypes = []interface{}{
	(*AccessCheckError)(nil),     // 0: gitaly.AccessCheckError
	(*MergeConflictError)(nil),   // 1: gitaly.MergeConflictError
	(*ReferenceUpdateError)(nil), // 2: gitaly.ReferenceUpdateError
	(*ResolveRevisionError)(nil), // 3: gitaly.ResolveRevisionError
}
var file_errors_proto_depIdxs = []int32{
	0, // [0:0] is the sub-list for method output_type
	0, // [0:0] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_errors_proto_init() }
func file_errors_proto_init() {
	if File_errors_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_errors_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*AccessCheckError); i {
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
		file_errors_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*MergeConflictError); i {
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
		file_errors_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ReferenceUpdateError); i {
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
		file_errors_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ResolveRevisionError); i {
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
			RawDescriptor: file_errors_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_errors_proto_goTypes,
		DependencyIndexes: file_errors_proto_depIdxs,
		MessageInfos:      file_errors_proto_msgTypes,
	}.Build()
	File_errors_proto = out.File
	file_errors_proto_rawDesc = nil
	file_errors_proto_goTypes = nil
	file_errors_proto_depIdxs = nil
}
