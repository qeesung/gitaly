// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.26.0
// 	protoc        v3.17.3
// source: cleanup.proto

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
type ApplyBfgObjectMapStreamRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Only available on the first message
	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// A raw object-map file as generated by BFG: https://rtyley.github.io/bfg-repo-cleaner
	// Each line in the file has two object SHAs, space-separated - the original
	// SHA of the object, and the SHA after BFG has rewritten the object.
	ObjectMap []byte `protobuf:"bytes,2,opt,name=object_map,json=objectMap,proto3" json:"object_map,omitempty"`
}

func (x *ApplyBfgObjectMapStreamRequest) Reset() {
	*x = ApplyBfgObjectMapStreamRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_cleanup_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ApplyBfgObjectMapStreamRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ApplyBfgObjectMapStreamRequest) ProtoMessage() {}

func (x *ApplyBfgObjectMapStreamRequest) ProtoReflect() protoreflect.Message {
	mi := &file_cleanup_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ApplyBfgObjectMapStreamRequest.ProtoReflect.Descriptor instead.
func (*ApplyBfgObjectMapStreamRequest) Descriptor() ([]byte, []int) {
	return file_cleanup_proto_rawDescGZIP(), []int{0}
}

func (x *ApplyBfgObjectMapStreamRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *ApplyBfgObjectMapStreamRequest) GetObjectMap() []byte {
	if x != nil {
		return x.ObjectMap
	}
	return nil
}

// This comment is left unintentionally blank.
type ApplyBfgObjectMapStreamResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// This comment is left unintentionally blank.
	Entries []*ApplyBfgObjectMapStreamResponse_Entry `protobuf:"bytes,1,rep,name=entries,proto3" json:"entries,omitempty"`
}

func (x *ApplyBfgObjectMapStreamResponse) Reset() {
	*x = ApplyBfgObjectMapStreamResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_cleanup_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ApplyBfgObjectMapStreamResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ApplyBfgObjectMapStreamResponse) ProtoMessage() {}

func (x *ApplyBfgObjectMapStreamResponse) ProtoReflect() protoreflect.Message {
	mi := &file_cleanup_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ApplyBfgObjectMapStreamResponse.ProtoReflect.Descriptor instead.
func (*ApplyBfgObjectMapStreamResponse) Descriptor() ([]byte, []int) {
	return file_cleanup_proto_rawDescGZIP(), []int{1}
}

func (x *ApplyBfgObjectMapStreamResponse) GetEntries() []*ApplyBfgObjectMapStreamResponse_Entry {
	if x != nil {
		return x.Entries
	}
	return nil
}

// We send back each parsed entry in the request's object map so the client
// can take action
type ApplyBfgObjectMapStreamResponse_Entry struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// This comment is left unintentionally blank.
	Type ObjectType `protobuf:"varint,1,opt,name=type,proto3,enum=gitaly.ObjectType" json:"type,omitempty"`
	// This comment is left unintentionally blank.
	OldOid string `protobuf:"bytes,2,opt,name=old_oid,json=oldOid,proto3" json:"old_oid,omitempty"`
	// This comment is left unintentionally blank.
	NewOid string `protobuf:"bytes,3,opt,name=new_oid,json=newOid,proto3" json:"new_oid,omitempty"`
}

func (x *ApplyBfgObjectMapStreamResponse_Entry) Reset() {
	*x = ApplyBfgObjectMapStreamResponse_Entry{}
	if protoimpl.UnsafeEnabled {
		mi := &file_cleanup_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ApplyBfgObjectMapStreamResponse_Entry) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ApplyBfgObjectMapStreamResponse_Entry) ProtoMessage() {}

func (x *ApplyBfgObjectMapStreamResponse_Entry) ProtoReflect() protoreflect.Message {
	mi := &file_cleanup_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ApplyBfgObjectMapStreamResponse_Entry.ProtoReflect.Descriptor instead.
func (*ApplyBfgObjectMapStreamResponse_Entry) Descriptor() ([]byte, []int) {
	return file_cleanup_proto_rawDescGZIP(), []int{1, 0}
}

func (x *ApplyBfgObjectMapStreamResponse_Entry) GetType() ObjectType {
	if x != nil {
		return x.Type
	}
	return ObjectType_UNKNOWN
}

func (x *ApplyBfgObjectMapStreamResponse_Entry) GetOldOid() string {
	if x != nil {
		return x.OldOid
	}
	return ""
}

func (x *ApplyBfgObjectMapStreamResponse_Entry) GetNewOid() string {
	if x != nil {
		return x.NewOid
	}
	return ""
}

var File_cleanup_proto protoreflect.FileDescriptor

var file_cleanup_proto_rawDesc = []byte{
	0x0a, 0x0d, 0x63, 0x6c, 0x65, 0x61, 0x6e, 0x75, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12,
	0x06, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x1a, 0x0a, 0x6c, 0x69, 0x6e, 0x74, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x1a, 0x0c, 0x73, 0x68, 0x61, 0x72, 0x65, 0x64, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x22, 0x79, 0x0a, 0x1e, 0x41, 0x70, 0x70, 0x6c, 0x79, 0x42, 0x66, 0x67, 0x4f, 0x62, 0x6a,
	0x65, 0x63, 0x74, 0x4d, 0x61, 0x70, 0x53, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x52, 0x65, 0x71, 0x75,
	0x65, 0x73, 0x74, 0x12, 0x38, 0x0a, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72,
	0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79,
	0x2e, 0x52, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c,
	0x01, 0x52, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x12, 0x1d, 0x0a,
	0x0a, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x5f, 0x6d, 0x61, 0x70, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0c, 0x52, 0x09, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x4d, 0x61, 0x70, 0x22, 0xcd, 0x01, 0x0a,
	0x1f, 0x41, 0x70, 0x70, 0x6c, 0x79, 0x42, 0x66, 0x67, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x4d,
	0x61, 0x70, 0x53, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x12, 0x47, 0x0a, 0x07, 0x65, 0x6e, 0x74, 0x72, 0x69, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28,
	0x0b, 0x32, 0x2d, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x41, 0x70, 0x70, 0x6c, 0x79,
	0x42, 0x66, 0x67, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x4d, 0x61, 0x70, 0x53, 0x74, 0x72, 0x65,
	0x61, 0x6d, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x2e, 0x45, 0x6e, 0x74, 0x72, 0x79,
	0x52, 0x07, 0x65, 0x6e, 0x74, 0x72, 0x69, 0x65, 0x73, 0x1a, 0x61, 0x0a, 0x05, 0x45, 0x6e, 0x74,
	0x72, 0x79, 0x12, 0x26, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e,
	0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74,
	0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x12, 0x17, 0x0a, 0x07, 0x6f, 0x6c,
	0x64, 0x5f, 0x6f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6f, 0x6c, 0x64,
	0x4f, 0x69, 0x64, 0x12, 0x17, 0x0a, 0x07, 0x6e, 0x65, 0x77, 0x5f, 0x6f, 0x69, 0x64, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6e, 0x65, 0x77, 0x4f, 0x69, 0x64, 0x32, 0x88, 0x01, 0x0a,
	0x0e, 0x43, 0x6c, 0x65, 0x61, 0x6e, 0x75, 0x70, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12,
	0x76, 0x0a, 0x17, 0x41, 0x70, 0x70, 0x6c, 0x79, 0x42, 0x66, 0x67, 0x4f, 0x62, 0x6a, 0x65, 0x63,
	0x74, 0x4d, 0x61, 0x70, 0x53, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x12, 0x26, 0x2e, 0x67, 0x69, 0x74,
	0x61, 0x6c, 0x79, 0x2e, 0x41, 0x70, 0x70, 0x6c, 0x79, 0x42, 0x66, 0x67, 0x4f, 0x62, 0x6a, 0x65,
	0x63, 0x74, 0x4d, 0x61, 0x70, 0x53, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x1a, 0x27, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x41, 0x70, 0x70, 0x6c,
	0x79, 0x42, 0x66, 0x67, 0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x4d, 0x61, 0x70, 0x53, 0x74, 0x72,
	0x65, 0x61, 0x6d, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa, 0x97, 0x28,
	0x02, 0x08, 0x01, 0x28, 0x01, 0x30, 0x01, 0x42, 0x34, 0x5a, 0x32, 0x67, 0x69, 0x74, 0x6c, 0x61,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2d, 0x6f, 0x72, 0x67,
	0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2f, 0x76, 0x31, 0x35, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2f, 0x67, 0x6f, 0x2f, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x70, 0x62, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_cleanup_proto_rawDescOnce sync.Once
	file_cleanup_proto_rawDescData = file_cleanup_proto_rawDesc
)

func file_cleanup_proto_rawDescGZIP() []byte {
	file_cleanup_proto_rawDescOnce.Do(func() {
		file_cleanup_proto_rawDescData = protoimpl.X.CompressGZIP(file_cleanup_proto_rawDescData)
	})
	return file_cleanup_proto_rawDescData
}

var file_cleanup_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_cleanup_proto_goTypes = []interface{}{
	(*ApplyBfgObjectMapStreamRequest)(nil),        // 0: gitaly.ApplyBfgObjectMapStreamRequest
	(*ApplyBfgObjectMapStreamResponse)(nil),       // 1: gitaly.ApplyBfgObjectMapStreamResponse
	(*ApplyBfgObjectMapStreamResponse_Entry)(nil), // 2: gitaly.ApplyBfgObjectMapStreamResponse.Entry
	(*Repository)(nil),                            // 3: gitaly.Repository
	(ObjectType)(0),                               // 4: gitaly.ObjectType
}
var file_cleanup_proto_depIdxs = []int32{
	3, // 0: gitaly.ApplyBfgObjectMapStreamRequest.repository:type_name -> gitaly.Repository
	2, // 1: gitaly.ApplyBfgObjectMapStreamResponse.entries:type_name -> gitaly.ApplyBfgObjectMapStreamResponse.Entry
	4, // 2: gitaly.ApplyBfgObjectMapStreamResponse.Entry.type:type_name -> gitaly.ObjectType
	0, // 3: gitaly.CleanupService.ApplyBfgObjectMapStream:input_type -> gitaly.ApplyBfgObjectMapStreamRequest
	1, // 4: gitaly.CleanupService.ApplyBfgObjectMapStream:output_type -> gitaly.ApplyBfgObjectMapStreamResponse
	4, // [4:5] is the sub-list for method output_type
	3, // [3:4] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_cleanup_proto_init() }
func file_cleanup_proto_init() {
	if File_cleanup_proto != nil {
		return
	}
	file_lint_proto_init()
	file_shared_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_cleanup_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ApplyBfgObjectMapStreamRequest); i {
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
		file_cleanup_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ApplyBfgObjectMapStreamResponse); i {
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
		file_cleanup_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ApplyBfgObjectMapStreamResponse_Entry); i {
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
			RawDescriptor: file_cleanup_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_cleanup_proto_goTypes,
		DependencyIndexes: file_cleanup_proto_depIdxs,
		MessageInfos:      file_cleanup_proto_msgTypes,
	}.Build()
	File_cleanup_proto = out.File
	file_cleanup_proto_rawDesc = nil
	file_cleanup_proto_goTypes = nil
	file_cleanup_proto_depIdxs = nil
}
