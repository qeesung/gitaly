// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.26.0
// 	protoc        v3.15.8
// source: transaction.proto

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

// The outcome of the given transaction telling the client whether the
// transaction should be committed or rolled back.
type VoteTransactionResponse_TransactionState int32

const (
	VoteTransactionResponse_COMMIT VoteTransactionResponse_TransactionState = 0
	VoteTransactionResponse_ABORT  VoteTransactionResponse_TransactionState = 1
	VoteTransactionResponse_STOP   VoteTransactionResponse_TransactionState = 2
)

// Enum value maps for VoteTransactionResponse_TransactionState.
var (
	VoteTransactionResponse_TransactionState_name = map[int32]string{
		0: "COMMIT",
		1: "ABORT",
		2: "STOP",
	}
	VoteTransactionResponse_TransactionState_value = map[string]int32{
		"COMMIT": 0,
		"ABORT":  1,
		"STOP":   2,
	}
)

func (x VoteTransactionResponse_TransactionState) Enum() *VoteTransactionResponse_TransactionState {
	p := new(VoteTransactionResponse_TransactionState)
	*p = x
	return p
}

func (x VoteTransactionResponse_TransactionState) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (VoteTransactionResponse_TransactionState) Descriptor() protoreflect.EnumDescriptor {
	return file_transaction_proto_enumTypes[0].Descriptor()
}

func (VoteTransactionResponse_TransactionState) Type() protoreflect.EnumType {
	return &file_transaction_proto_enumTypes[0]
}

func (x VoteTransactionResponse_TransactionState) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use VoteTransactionResponse_TransactionState.Descriptor instead.
func (VoteTransactionResponse_TransactionState) EnumDescriptor() ([]byte, []int) {
	return file_transaction_proto_rawDescGZIP(), []int{1, 0}
}

type VoteTransactionRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// ID of the transaction we're processing
	TransactionId uint64 `protobuf:"varint,2,opt,name=transaction_id,json=transactionId,proto3" json:"transaction_id,omitempty"`
	// Name of the Gitaly node that's voting on a transaction.
	Node string `protobuf:"bytes,3,opt,name=node,proto3" json:"node,omitempty"`
	// SHA1 of the references that are to be updated
	ReferenceUpdatesHash []byte `protobuf:"bytes,4,opt,name=reference_updates_hash,json=referenceUpdatesHash,proto3" json:"reference_updates_hash,omitempty"`
}

func (x *VoteTransactionRequest) Reset() {
	*x = VoteTransactionRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_transaction_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *VoteTransactionRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*VoteTransactionRequest) ProtoMessage() {}

func (x *VoteTransactionRequest) ProtoReflect() protoreflect.Message {
	mi := &file_transaction_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use VoteTransactionRequest.ProtoReflect.Descriptor instead.
func (*VoteTransactionRequest) Descriptor() ([]byte, []int) {
	return file_transaction_proto_rawDescGZIP(), []int{0}
}

func (x *VoteTransactionRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *VoteTransactionRequest) GetTransactionId() uint64 {
	if x != nil {
		return x.TransactionId
	}
	return 0
}

func (x *VoteTransactionRequest) GetNode() string {
	if x != nil {
		return x.Node
	}
	return ""
}

func (x *VoteTransactionRequest) GetReferenceUpdatesHash() []byte {
	if x != nil {
		return x.ReferenceUpdatesHash
	}
	return nil
}

type VoteTransactionResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	State VoteTransactionResponse_TransactionState `protobuf:"varint,1,opt,name=state,proto3,enum=gitaly.VoteTransactionResponse_TransactionState" json:"state,omitempty"`
}

func (x *VoteTransactionResponse) Reset() {
	*x = VoteTransactionResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_transaction_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *VoteTransactionResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*VoteTransactionResponse) ProtoMessage() {}

func (x *VoteTransactionResponse) ProtoReflect() protoreflect.Message {
	mi := &file_transaction_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use VoteTransactionResponse.ProtoReflect.Descriptor instead.
func (*VoteTransactionResponse) Descriptor() ([]byte, []int) {
	return file_transaction_proto_rawDescGZIP(), []int{1}
}

func (x *VoteTransactionResponse) GetState() VoteTransactionResponse_TransactionState {
	if x != nil {
		return x.State
	}
	return VoteTransactionResponse_COMMIT
}

type StopTransactionRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Repository *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	// ID of the transaction we're processing
	TransactionId uint64 `protobuf:"varint,2,opt,name=transaction_id,json=transactionId,proto3" json:"transaction_id,omitempty"`
}

func (x *StopTransactionRequest) Reset() {
	*x = StopTransactionRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_transaction_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *StopTransactionRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StopTransactionRequest) ProtoMessage() {}

func (x *StopTransactionRequest) ProtoReflect() protoreflect.Message {
	mi := &file_transaction_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StopTransactionRequest.ProtoReflect.Descriptor instead.
func (*StopTransactionRequest) Descriptor() ([]byte, []int) {
	return file_transaction_proto_rawDescGZIP(), []int{2}
}

func (x *StopTransactionRequest) GetRepository() *Repository {
	if x != nil {
		return x.Repository
	}
	return nil
}

func (x *StopTransactionRequest) GetTransactionId() uint64 {
	if x != nil {
		return x.TransactionId
	}
	return 0
}

type StopTransactionResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *StopTransactionResponse) Reset() {
	*x = StopTransactionResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_transaction_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *StopTransactionResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StopTransactionResponse) ProtoMessage() {}

func (x *StopTransactionResponse) ProtoReflect() protoreflect.Message {
	mi := &file_transaction_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StopTransactionResponse.ProtoReflect.Descriptor instead.
func (*StopTransactionResponse) Descriptor() ([]byte, []int) {
	return file_transaction_proto_rawDescGZIP(), []int{3}
}

var File_transaction_proto protoreflect.FileDescriptor

var file_transaction_proto_rawDesc = []byte{
	0x0a, 0x11, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x12, 0x06, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x1a, 0x0a, 0x6c, 0x69, 0x6e,
	0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x0c, 0x73, 0x68, 0x61, 0x72, 0x65, 0x64, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xc3, 0x01, 0x0a, 0x16, 0x56, 0x6f, 0x74, 0x65, 0x54, 0x72,
	0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x12, 0x38, 0x0a, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65,
	0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a,
	0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x12, 0x25, 0x0a, 0x0e, 0x74, 0x72,
	0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x04, 0x52, 0x0d, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x49,
	0x64, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x6f, 0x64, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x6e, 0x6f, 0x64, 0x65, 0x12, 0x34, 0x0a, 0x16, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e,
	0x63, 0x65, 0x5f, 0x75, 0x70, 0x64, 0x61, 0x74, 0x65, 0x73, 0x5f, 0x68, 0x61, 0x73, 0x68, 0x18,
	0x04, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x14, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65,
	0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x73, 0x48, 0x61, 0x73, 0x68, 0x22, 0x96, 0x01, 0x0a, 0x17,
	0x56, 0x6f, 0x74, 0x65, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x52,
	0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x46, 0x0a, 0x05, 0x73, 0x74, 0x61, 0x74, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x30, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e,
	0x56, 0x6f, 0x74, 0x65, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x52,
	0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x2e, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74,
	0x69, 0x6f, 0x6e, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x05, 0x73, 0x74, 0x61, 0x74, 0x65, 0x22,
	0x33, 0x0a, 0x10, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x53, 0x74,
	0x61, 0x74, 0x65, 0x12, 0x0a, 0x0a, 0x06, 0x43, 0x4f, 0x4d, 0x4d, 0x49, 0x54, 0x10, 0x00, 0x12,
	0x09, 0x0a, 0x05, 0x41, 0x42, 0x4f, 0x52, 0x54, 0x10, 0x01, 0x12, 0x08, 0x0a, 0x04, 0x53, 0x54,
	0x4f, 0x50, 0x10, 0x02, 0x22, 0x79, 0x0a, 0x16, 0x53, 0x74, 0x6f, 0x70, 0x54, 0x72, 0x61, 0x6e,
	0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x38,
	0x0a, 0x0a, 0x72, 0x65, 0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x52, 0x65, 0x70, 0x6f,
	0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x42, 0x04, 0x98, 0xc6, 0x2c, 0x01, 0x52, 0x0a, 0x72, 0x65,
	0x70, 0x6f, 0x73, 0x69, 0x74, 0x6f, 0x72, 0x79, 0x12, 0x25, 0x0a, 0x0e, 0x74, 0x72, 0x61, 0x6e,
	0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x04,
	0x52, 0x0d, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x49, 0x64, 0x22,
	0x19, 0x0a, 0x17, 0x53, 0x74, 0x6f, 0x70, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69,
	0x6f, 0x6e, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x32, 0xc8, 0x01, 0x0a, 0x0e, 0x52,
	0x65, 0x66, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x5a, 0x0a,
	0x0f, 0x56, 0x6f, 0x74, 0x65, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e,
	0x12, 0x1e, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x56, 0x6f, 0x74, 0x65, 0x54, 0x72,
	0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x1a, 0x1f, 0x2e, 0x67, 0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x56, 0x6f, 0x74, 0x65, 0x54, 0x72,
	0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x22, 0x06, 0xfa, 0x97, 0x28, 0x02, 0x08, 0x01, 0x12, 0x5a, 0x0a, 0x0f, 0x53, 0x74, 0x6f,
	0x70, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x1e, 0x2e, 0x67,
	0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x53, 0x74, 0x6f, 0x70, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61,
	0x63, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1f, 0x2e, 0x67,
	0x69, 0x74, 0x61, 0x6c, 0x79, 0x2e, 0x53, 0x74, 0x6f, 0x70, 0x54, 0x72, 0x61, 0x6e, 0x73, 0x61,
	0x63, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x06, 0xfa,
	0x97, 0x28, 0x02, 0x08, 0x01, 0x42, 0x30, 0x5a, 0x2e, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2e,
	0x63, 0x6f, 0x6d, 0x2f, 0x67, 0x69, 0x74, 0x6c, 0x61, 0x62, 0x2d, 0x6f, 0x72, 0x67, 0x2f, 0x67,
	0x69, 0x74, 0x61, 0x6c, 0x79, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x67, 0x6f, 0x2f, 0x67,
	0x69, 0x74, 0x61, 0x6c, 0x79, 0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_transaction_proto_rawDescOnce sync.Once
	file_transaction_proto_rawDescData = file_transaction_proto_rawDesc
)

func file_transaction_proto_rawDescGZIP() []byte {
	file_transaction_proto_rawDescOnce.Do(func() {
		file_transaction_proto_rawDescData = protoimpl.X.CompressGZIP(file_transaction_proto_rawDescData)
	})
	return file_transaction_proto_rawDescData
}

var file_transaction_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_transaction_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_transaction_proto_goTypes = []interface{}{
	(VoteTransactionResponse_TransactionState)(0), // 0: gitaly.VoteTransactionResponse.TransactionState
	(*VoteTransactionRequest)(nil),                // 1: gitaly.VoteTransactionRequest
	(*VoteTransactionResponse)(nil),               // 2: gitaly.VoteTransactionResponse
	(*StopTransactionRequest)(nil),                // 3: gitaly.StopTransactionRequest
	(*StopTransactionResponse)(nil),               // 4: gitaly.StopTransactionResponse
	(*Repository)(nil),                            // 5: gitaly.Repository
}
var file_transaction_proto_depIdxs = []int32{
	5, // 0: gitaly.VoteTransactionRequest.repository:type_name -> gitaly.Repository
	0, // 1: gitaly.VoteTransactionResponse.state:type_name -> gitaly.VoteTransactionResponse.TransactionState
	5, // 2: gitaly.StopTransactionRequest.repository:type_name -> gitaly.Repository
	1, // 3: gitaly.RefTransaction.VoteTransaction:input_type -> gitaly.VoteTransactionRequest
	3, // 4: gitaly.RefTransaction.StopTransaction:input_type -> gitaly.StopTransactionRequest
	2, // 5: gitaly.RefTransaction.VoteTransaction:output_type -> gitaly.VoteTransactionResponse
	4, // 6: gitaly.RefTransaction.StopTransaction:output_type -> gitaly.StopTransactionResponse
	5, // [5:7] is the sub-list for method output_type
	3, // [3:5] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_transaction_proto_init() }
func file_transaction_proto_init() {
	if File_transaction_proto != nil {
		return
	}
	file_lint_proto_init()
	file_shared_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_transaction_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*VoteTransactionRequest); i {
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
		file_transaction_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*VoteTransactionResponse); i {
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
		file_transaction_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*StopTransactionRequest); i {
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
		file_transaction_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*StopTransactionResponse); i {
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
			RawDescriptor: file_transaction_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_transaction_proto_goTypes,
		DependencyIndexes: file_transaction_proto_depIdxs,
		EnumInfos:         file_transaction_proto_enumTypes,
		MessageInfos:      file_transaction_proto_msgTypes,
	}.Build()
	File_transaction_proto = out.File
	file_transaction_proto_rawDesc = nil
	file_transaction_proto_goTypes = nil
	file_transaction_proto_depIdxs = nil
}
