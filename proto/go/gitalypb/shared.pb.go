// Code generated by protoc-gen-go. DO NOT EDIT.
// source: shared.proto

package gitalypb

import (
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
	descriptor "github.com/golang/protobuf/protoc-gen-go/descriptor"
	timestamp "github.com/golang/protobuf/ptypes/timestamp"
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

type ObjectType int32

const (
	ObjectType_UNKNOWN ObjectType = 0
	ObjectType_COMMIT  ObjectType = 1
	ObjectType_BLOB    ObjectType = 2
	ObjectType_TREE    ObjectType = 3
	ObjectType_TAG     ObjectType = 4
)

var ObjectType_name = map[int32]string{
	0: "UNKNOWN",
	1: "COMMIT",
	2: "BLOB",
	3: "TREE",
	4: "TAG",
}

var ObjectType_value = map[string]int32{
	"UNKNOWN": 0,
	"COMMIT":  1,
	"BLOB":    2,
	"TREE":    3,
	"TAG":     4,
}

func (x ObjectType) String() string {
	return proto.EnumName(ObjectType_name, int32(x))
}

func (ObjectType) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{0}
}

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
	return fileDescriptor_d8a4e87e678c5ced, []int{0, 0}
}

type OperationMsg_Scope int32

const (
	OperationMsg_REPOSITORY OperationMsg_Scope = 0
	OperationMsg_SERVER     OperationMsg_Scope = 1
	OperationMsg_STORAGE    OperationMsg_Scope = 2
)

var OperationMsg_Scope_name = map[int32]string{
	0: "REPOSITORY",
	1: "SERVER",
	2: "STORAGE",
}

var OperationMsg_Scope_value = map[string]int32{
	"REPOSITORY": 0,
	"SERVER":     1,
	"STORAGE":    2,
}

func (x OperationMsg_Scope) String() string {
	return proto.EnumName(OperationMsg_Scope_name, int32(x))
}

func (OperationMsg_Scope) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{0, 1}
}

type OperationMsg struct {
	Op OperationMsg_Operation `protobuf:"varint,1,opt,name=op,proto3,enum=gitaly.OperationMsg_Operation" json:"op,omitempty"`
	// Scope level indicates what level an RPC interacts with a server:
	//   - REPOSITORY: scoped to only a single repo
	//   - SERVER: affects the entire server and potentially all repos
	//   - STORAGE: scoped to a specific storage location and all repos within
	ScopeLevel OperationMsg_Scope `protobuf:"varint,2,opt,name=scope_level,json=scopeLevel,proto3,enum=gitaly.OperationMsg_Scope" json:"scope_level,omitempty"`
	// If this operation modifies a repository, this field will
	// specify the location of the Repository field within the
	// request message. The field is specified in an OID style
	// formatted string.
	//
	// For example, if the target repository is at the top level
	// of a message at field 1, then the string will be "1"
	//
	// If the target repository is nested deeper in the message,
	// then it will be necessary to specify a nested OID string.
	//
	// For example, the following OID refers to a target repo field
	// nested in a one-of field, both at field one: "1.1"
	TargetRepositoryField     string   `protobuf:"bytes,3,opt,name=target_repository_field,json=targetRepositoryField,proto3" json:"target_repository_field,omitempty"`
	AdditionalRepositoryField string   `protobuf:"bytes,4,opt,name=additional_repository_field,json=additionalRepositoryField,proto3" json:"additional_repository_field,omitempty"`
	XXX_NoUnkeyedLiteral      struct{} `json:"-"`
	XXX_unrecognized          []byte   `json:"-"`
	XXX_sizecache             int32    `json:"-"`
}

func (m *OperationMsg) Reset()         { *m = OperationMsg{} }
func (m *OperationMsg) String() string { return proto.CompactTextString(m) }
func (*OperationMsg) ProtoMessage()    {}
func (*OperationMsg) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{0}
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

func (m *OperationMsg) GetTargetRepositoryField() string {
	if m != nil {
		return m.TargetRepositoryField
	}
	return ""
}

func (m *OperationMsg) GetAdditionalRepositoryField() string {
	if m != nil {
		return m.AdditionalRepositoryField
	}
	return ""
}

type Repository struct {
	StorageName  string `protobuf:"bytes,2,opt,name=storage_name,json=storageName,proto3" json:"storage_name,omitempty"`
	RelativePath string `protobuf:"bytes,3,opt,name=relative_path,json=relativePath,proto3" json:"relative_path,omitempty"`
	// Sets the GIT_OBJECT_DIRECTORY envvar on git commands to the value of this field.
	// It influences the object storage directory the SHA1 directories are created underneath.
	GitObjectDirectory string `protobuf:"bytes,4,opt,name=git_object_directory,json=gitObjectDirectory,proto3" json:"git_object_directory,omitempty"`
	// Sets the GIT_ALTERNATE_OBJECT_DIRECTORIES envvar on git commands to the values of this field.
	// It influences the list of Git object directories which can be used to search for Git objects.
	GitAlternateObjectDirectories []string `protobuf:"bytes,5,rep,name=git_alternate_object_directories,json=gitAlternateObjectDirectories,proto3" json:"git_alternate_object_directories,omitempty"`
	// Used in callbacks to GitLab so that it knows what repository the event is
	// associated with. May be left empty on RPC's that do not perform callbacks.
	// During project creation, `gl_repository` may not be known.
	GlRepository string `protobuf:"bytes,6,opt,name=gl_repository,json=glRepository,proto3" json:"gl_repository,omitempty"`
	// The human-readable GitLab project path (e.g. gitlab-org/gitlab-ce).
	// When hashed storage is use, this associates a project path with its
	// path on disk. The name can change over time (e.g. when a project is
	// renamed). This is primarily used for logging/debugging at the
	// moment.
	GlProjectPath        string   `protobuf:"bytes,8,opt,name=gl_project_path,json=glProjectPath,proto3" json:"gl_project_path,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *Repository) Reset()         { *m = Repository{} }
func (m *Repository) String() string { return proto.CompactTextString(m) }
func (*Repository) ProtoMessage()    {}
func (*Repository) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{1}
}

func (m *Repository) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Repository.Unmarshal(m, b)
}
func (m *Repository) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Repository.Marshal(b, m, deterministic)
}
func (m *Repository) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Repository.Merge(m, src)
}
func (m *Repository) XXX_Size() int {
	return xxx_messageInfo_Repository.Size(m)
}
func (m *Repository) XXX_DiscardUnknown() {
	xxx_messageInfo_Repository.DiscardUnknown(m)
}

var xxx_messageInfo_Repository proto.InternalMessageInfo

func (m *Repository) GetStorageName() string {
	if m != nil {
		return m.StorageName
	}
	return ""
}

func (m *Repository) GetRelativePath() string {
	if m != nil {
		return m.RelativePath
	}
	return ""
}

func (m *Repository) GetGitObjectDirectory() string {
	if m != nil {
		return m.GitObjectDirectory
	}
	return ""
}

func (m *Repository) GetGitAlternateObjectDirectories() []string {
	if m != nil {
		return m.GitAlternateObjectDirectories
	}
	return nil
}

func (m *Repository) GetGlRepository() string {
	if m != nil {
		return m.GlRepository
	}
	return ""
}

func (m *Repository) GetGlProjectPath() string {
	if m != nil {
		return m.GlProjectPath
	}
	return ""
}

// Corresponds to Gitlab::Git::Commit
type GitCommit struct {
	Id        string        `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	Subject   []byte        `protobuf:"bytes,2,opt,name=subject,proto3" json:"subject,omitempty"`
	Body      []byte        `protobuf:"bytes,3,opt,name=body,proto3" json:"body,omitempty"`
	Author    *CommitAuthor `protobuf:"bytes,4,opt,name=author,proto3" json:"author,omitempty"`
	Committer *CommitAuthor `protobuf:"bytes,5,opt,name=committer,proto3" json:"committer,omitempty"`
	ParentIds []string      `protobuf:"bytes,6,rep,name=parent_ids,json=parentIds,proto3" json:"parent_ids,omitempty"`
	// If body exceeds a certain threshold, it will be nullified,
	// but its size will be set in body_size so we can know if
	// a commit had a body in the first place.
	BodySize             int64    `protobuf:"varint,7,opt,name=body_size,json=bodySize,proto3" json:"body_size,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *GitCommit) Reset()         { *m = GitCommit{} }
func (m *GitCommit) String() string { return proto.CompactTextString(m) }
func (*GitCommit) ProtoMessage()    {}
func (*GitCommit) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{2}
}

func (m *GitCommit) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_GitCommit.Unmarshal(m, b)
}
func (m *GitCommit) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_GitCommit.Marshal(b, m, deterministic)
}
func (m *GitCommit) XXX_Merge(src proto.Message) {
	xxx_messageInfo_GitCommit.Merge(m, src)
}
func (m *GitCommit) XXX_Size() int {
	return xxx_messageInfo_GitCommit.Size(m)
}
func (m *GitCommit) XXX_DiscardUnknown() {
	xxx_messageInfo_GitCommit.DiscardUnknown(m)
}

var xxx_messageInfo_GitCommit proto.InternalMessageInfo

func (m *GitCommit) GetId() string {
	if m != nil {
		return m.Id
	}
	return ""
}

func (m *GitCommit) GetSubject() []byte {
	if m != nil {
		return m.Subject
	}
	return nil
}

func (m *GitCommit) GetBody() []byte {
	if m != nil {
		return m.Body
	}
	return nil
}

func (m *GitCommit) GetAuthor() *CommitAuthor {
	if m != nil {
		return m.Author
	}
	return nil
}

func (m *GitCommit) GetCommitter() *CommitAuthor {
	if m != nil {
		return m.Committer
	}
	return nil
}

func (m *GitCommit) GetParentIds() []string {
	if m != nil {
		return m.ParentIds
	}
	return nil
}

func (m *GitCommit) GetBodySize() int64 {
	if m != nil {
		return m.BodySize
	}
	return 0
}

type CommitAuthor struct {
	Name                 []byte               `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Email                []byte               `protobuf:"bytes,2,opt,name=email,proto3" json:"email,omitempty"`
	Date                 *timestamp.Timestamp `protobuf:"bytes,3,opt,name=date,proto3" json:"date,omitempty"`
	Tz                   []byte               `protobuf:"bytes,4,opt,name=tz,proto3" json:"tz,omitempty"`
	XXX_NoUnkeyedLiteral struct{}             `json:"-"`
	XXX_unrecognized     []byte               `json:"-"`
	XXX_sizecache        int32                `json:"-"`
}

func (m *CommitAuthor) Reset()         { *m = CommitAuthor{} }
func (m *CommitAuthor) String() string { return proto.CompactTextString(m) }
func (*CommitAuthor) ProtoMessage()    {}
func (*CommitAuthor) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{3}
}

func (m *CommitAuthor) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_CommitAuthor.Unmarshal(m, b)
}
func (m *CommitAuthor) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_CommitAuthor.Marshal(b, m, deterministic)
}
func (m *CommitAuthor) XXX_Merge(src proto.Message) {
	xxx_messageInfo_CommitAuthor.Merge(m, src)
}
func (m *CommitAuthor) XXX_Size() int {
	return xxx_messageInfo_CommitAuthor.Size(m)
}
func (m *CommitAuthor) XXX_DiscardUnknown() {
	xxx_messageInfo_CommitAuthor.DiscardUnknown(m)
}

var xxx_messageInfo_CommitAuthor proto.InternalMessageInfo

func (m *CommitAuthor) GetName() []byte {
	if m != nil {
		return m.Name
	}
	return nil
}

func (m *CommitAuthor) GetEmail() []byte {
	if m != nil {
		return m.Email
	}
	return nil
}

func (m *CommitAuthor) GetDate() *timestamp.Timestamp {
	if m != nil {
		return m.Date
	}
	return nil
}

func (m *CommitAuthor) GetTz() []byte {
	if m != nil {
		return m.Tz
	}
	return nil
}

type ExitStatus struct {
	Value                int32    `protobuf:"varint,1,opt,name=value,proto3" json:"value,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *ExitStatus) Reset()         { *m = ExitStatus{} }
func (m *ExitStatus) String() string { return proto.CompactTextString(m) }
func (*ExitStatus) ProtoMessage()    {}
func (*ExitStatus) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{4}
}

func (m *ExitStatus) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ExitStatus.Unmarshal(m, b)
}
func (m *ExitStatus) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ExitStatus.Marshal(b, m, deterministic)
}
func (m *ExitStatus) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ExitStatus.Merge(m, src)
}
func (m *ExitStatus) XXX_Size() int {
	return xxx_messageInfo_ExitStatus.Size(m)
}
func (m *ExitStatus) XXX_DiscardUnknown() {
	xxx_messageInfo_ExitStatus.DiscardUnknown(m)
}

var xxx_messageInfo_ExitStatus proto.InternalMessageInfo

func (m *ExitStatus) GetValue() int32 {
	if m != nil {
		return m.Value
	}
	return 0
}

// Corresponds to Gitlab::Git::Branch
type Branch struct {
	Name                 []byte     `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	TargetCommit         *GitCommit `protobuf:"bytes,2,opt,name=target_commit,json=targetCommit,proto3" json:"target_commit,omitempty"`
	XXX_NoUnkeyedLiteral struct{}   `json:"-"`
	XXX_unrecognized     []byte     `json:"-"`
	XXX_sizecache        int32      `json:"-"`
}

func (m *Branch) Reset()         { *m = Branch{} }
func (m *Branch) String() string { return proto.CompactTextString(m) }
func (*Branch) ProtoMessage()    {}
func (*Branch) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{5}
}

func (m *Branch) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Branch.Unmarshal(m, b)
}
func (m *Branch) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Branch.Marshal(b, m, deterministic)
}
func (m *Branch) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Branch.Merge(m, src)
}
func (m *Branch) XXX_Size() int {
	return xxx_messageInfo_Branch.Size(m)
}
func (m *Branch) XXX_DiscardUnknown() {
	xxx_messageInfo_Branch.DiscardUnknown(m)
}

var xxx_messageInfo_Branch proto.InternalMessageInfo

func (m *Branch) GetName() []byte {
	if m != nil {
		return m.Name
	}
	return nil
}

func (m *Branch) GetTargetCommit() *GitCommit {
	if m != nil {
		return m.TargetCommit
	}
	return nil
}

type Tag struct {
	Name         []byte     `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Id           string     `protobuf:"bytes,2,opt,name=id,proto3" json:"id,omitempty"`
	TargetCommit *GitCommit `protobuf:"bytes,3,opt,name=target_commit,json=targetCommit,proto3" json:"target_commit,omitempty"`
	// If message exceeds a certain threshold, it will be nullified,
	// but its size will be set in message_size so we can know if
	// a tag had a message in the first place.
	Message              []byte        `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
	MessageSize          int64         `protobuf:"varint,5,opt,name=message_size,json=messageSize,proto3" json:"message_size,omitempty"`
	Tagger               *CommitAuthor `protobuf:"bytes,6,opt,name=tagger,proto3" json:"tagger,omitempty"`
	XXX_NoUnkeyedLiteral struct{}      `json:"-"`
	XXX_unrecognized     []byte        `json:"-"`
	XXX_sizecache        int32         `json:"-"`
}

func (m *Tag) Reset()         { *m = Tag{} }
func (m *Tag) String() string { return proto.CompactTextString(m) }
func (*Tag) ProtoMessage()    {}
func (*Tag) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{6}
}

func (m *Tag) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Tag.Unmarshal(m, b)
}
func (m *Tag) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Tag.Marshal(b, m, deterministic)
}
func (m *Tag) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Tag.Merge(m, src)
}
func (m *Tag) XXX_Size() int {
	return xxx_messageInfo_Tag.Size(m)
}
func (m *Tag) XXX_DiscardUnknown() {
	xxx_messageInfo_Tag.DiscardUnknown(m)
}

var xxx_messageInfo_Tag proto.InternalMessageInfo

func (m *Tag) GetName() []byte {
	if m != nil {
		return m.Name
	}
	return nil
}

func (m *Tag) GetId() string {
	if m != nil {
		return m.Id
	}
	return ""
}

func (m *Tag) GetTargetCommit() *GitCommit {
	if m != nil {
		return m.TargetCommit
	}
	return nil
}

func (m *Tag) GetMessage() []byte {
	if m != nil {
		return m.Message
	}
	return nil
}

func (m *Tag) GetMessageSize() int64 {
	if m != nil {
		return m.MessageSize
	}
	return 0
}

func (m *Tag) GetTagger() *CommitAuthor {
	if m != nil {
		return m.Tagger
	}
	return nil
}

type User struct {
	GlId                 string   `protobuf:"bytes,1,opt,name=gl_id,json=glId,proto3" json:"gl_id,omitempty"`
	Name                 []byte   `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Email                []byte   `protobuf:"bytes,3,opt,name=email,proto3" json:"email,omitempty"`
	GlUsername           string   `protobuf:"bytes,4,opt,name=gl_username,json=glUsername,proto3" json:"gl_username,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *User) Reset()         { *m = User{} }
func (m *User) String() string { return proto.CompactTextString(m) }
func (*User) ProtoMessage()    {}
func (*User) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{7}
}

func (m *User) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_User.Unmarshal(m, b)
}
func (m *User) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_User.Marshal(b, m, deterministic)
}
func (m *User) XXX_Merge(src proto.Message) {
	xxx_messageInfo_User.Merge(m, src)
}
func (m *User) XXX_Size() int {
	return xxx_messageInfo_User.Size(m)
}
func (m *User) XXX_DiscardUnknown() {
	xxx_messageInfo_User.DiscardUnknown(m)
}

var xxx_messageInfo_User proto.InternalMessageInfo

func (m *User) GetGlId() string {
	if m != nil {
		return m.GlId
	}
	return ""
}

func (m *User) GetName() []byte {
	if m != nil {
		return m.Name
	}
	return nil
}

func (m *User) GetEmail() []byte {
	if m != nil {
		return m.Email
	}
	return nil
}

func (m *User) GetGlUsername() string {
	if m != nil {
		return m.GlUsername
	}
	return ""
}

type ObjectPool struct {
	Repository           *Repository `protobuf:"bytes,1,opt,name=repository,proto3" json:"repository,omitempty"`
	XXX_NoUnkeyedLiteral struct{}    `json:"-"`
	XXX_unrecognized     []byte      `json:"-"`
	XXX_sizecache        int32       `json:"-"`
}

func (m *ObjectPool) Reset()         { *m = ObjectPool{} }
func (m *ObjectPool) String() string { return proto.CompactTextString(m) }
func (*ObjectPool) ProtoMessage()    {}
func (*ObjectPool) Descriptor() ([]byte, []int) {
	return fileDescriptor_d8a4e87e678c5ced, []int{8}
}

func (m *ObjectPool) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ObjectPool.Unmarshal(m, b)
}
func (m *ObjectPool) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ObjectPool.Marshal(b, m, deterministic)
}
func (m *ObjectPool) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ObjectPool.Merge(m, src)
}
func (m *ObjectPool) XXX_Size() int {
	return xxx_messageInfo_ObjectPool.Size(m)
}
func (m *ObjectPool) XXX_DiscardUnknown() {
	xxx_messageInfo_ObjectPool.DiscardUnknown(m)
}

var xxx_messageInfo_ObjectPool proto.InternalMessageInfo

func (m *ObjectPool) GetRepository() *Repository {
	if m != nil {
		return m.Repository
	}
	return nil
}

var E_OpType = &proto.ExtensionDesc{
	ExtendedType:  (*descriptor.MethodOptions)(nil),
	ExtensionType: (*OperationMsg)(nil),
	Field:         82303,
	Name:          "gitaly.op_type",
	Tag:           "bytes,82303,opt,name=op_type",
	Filename:      "shared.proto",
}

func init() {
	proto.RegisterEnum("gitaly.ObjectType", ObjectType_name, ObjectType_value)
	proto.RegisterEnum("gitaly.OperationMsg_Operation", OperationMsg_Operation_name, OperationMsg_Operation_value)
	proto.RegisterEnum("gitaly.OperationMsg_Scope", OperationMsg_Scope_name, OperationMsg_Scope_value)
	proto.RegisterType((*OperationMsg)(nil), "gitaly.OperationMsg")
	proto.RegisterType((*Repository)(nil), "gitaly.Repository")
	proto.RegisterType((*GitCommit)(nil), "gitaly.GitCommit")
	proto.RegisterType((*CommitAuthor)(nil), "gitaly.CommitAuthor")
	proto.RegisterType((*ExitStatus)(nil), "gitaly.ExitStatus")
	proto.RegisterType((*Branch)(nil), "gitaly.Branch")
	proto.RegisterType((*Tag)(nil), "gitaly.Tag")
	proto.RegisterType((*User)(nil), "gitaly.User")
	proto.RegisterType((*ObjectPool)(nil), "gitaly.ObjectPool")
	proto.RegisterExtension(E_OpType)
}

func init() { proto.RegisterFile("shared.proto", fileDescriptor_d8a4e87e678c5ced) }

var fileDescriptor_d8a4e87e678c5ced = []byte{
	// 928 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x8c, 0x55, 0xef, 0x6e, 0xe3, 0x44,
	0x10, 0x3f, 0x3b, 0xce, 0xbf, 0x89, 0x5b, 0xcc, 0x52, 0x84, 0xe9, 0xe9, 0xee, 0x82, 0x91, 0x50,
	0x85, 0x20, 0xad, 0x7a, 0xd2, 0x7d, 0x00, 0x09, 0x91, 0x96, 0x50, 0xf5, 0xb8, 0xd6, 0xd5, 0xc6,
	0x05, 0xc1, 0x17, 0x6b, 0x13, 0x6f, 0x37, 0x8b, 0xec, 0xae, 0xb5, 0xde, 0x54, 0xd7, 0x7e, 0xe4,
	0x81, 0x78, 0x12, 0x9e, 0x80, 0x17, 0xe0, 0x31, 0x40, 0xbb, 0x6b, 0x27, 0xb9, 0xb6, 0x20, 0xbe,
	0xed, 0xcc, 0xfc, 0xe6, 0xdf, 0x6f, 0x66, 0x6c, 0xf0, 0xab, 0x05, 0x91, 0x34, 0x1b, 0x95, 0x52,
	0x28, 0x81, 0x3a, 0x8c, 0x2b, 0x92, 0xdf, 0xee, 0xbe, 0x60, 0x42, 0xb0, 0x9c, 0xee, 0x1b, 0xed,
	0x6c, 0x79, 0xb5, 0xaf, 0x78, 0x41, 0x2b, 0x45, 0x8a, 0xd2, 0x02, 0x77, 0x87, 0xf7, 0x01, 0x19,
	0xad, 0xe6, 0x92, 0x97, 0x4a, 0x48, 0x8b, 0x88, 0xfe, 0x74, 0xc1, 0x8f, 0x4b, 0x2a, 0x89, 0xe2,
	0xe2, 0xfa, 0xac, 0x62, 0x68, 0x04, 0xae, 0x28, 0x43, 0x67, 0xe8, 0xec, 0x6d, 0x1f, 0x3e, 0x1f,
	0xd9, 0x44, 0xa3, 0x4d, 0xc4, 0x5a, 0xc0, 0xae, 0x28, 0xd1, 0xd7, 0x30, 0xa8, 0xe6, 0xa2, 0xa4,
	0x69, 0x4e, 0x6f, 0x68, 0x1e, 0xba, 0xc6, 0x71, 0xf7, 0x51, 0xc7, 0xa9, 0xc6, 0x61, 0x30, 0xf0,
	0x37, 0x1a, 0x8d, 0x5e, 0xc1, 0x47, 0x8a, 0x48, 0x46, 0x55, 0x2a, 0x69, 0x29, 0x2a, 0xae, 0x84,
	0xbc, 0x4d, 0xaf, 0x38, 0xcd, 0xb3, 0xb0, 0x35, 0x74, 0xf6, 0xfa, 0xf8, 0x43, 0x6b, 0xc6, 0x2b,
	0xeb, 0xf7, 0xda, 0x88, 0xbe, 0x81, 0xa7, 0x24, 0xcb, 0xb8, 0x0e, 0x4c, 0xf2, 0x87, 0xbe, 0x9e,
	0xf1, 0xfd, 0x78, 0x0d, 0xb9, 0xe7, 0x1f, 0xbd, 0x84, 0xfe, 0xaa, 0x32, 0x34, 0x80, 0xee, 0xe5,
	0xf9, 0x0f, 0xe7, 0xf1, 0x4f, 0xe7, 0xc1, 0x13, 0x2d, 0x9c, 0x5d, 0x26, 0xe3, 0x24, 0xc6, 0x81,
	0x83, 0x7c, 0xe8, 0x8d, 0x8f, 0x8f, 0x27, 0xd3, 0x69, 0x8c, 0x03, 0x37, 0x3a, 0x80, 0xb6, 0xe9,
	0x00, 0x6d, 0x03, 0xe0, 0xc9, 0x45, 0x3c, 0x3d, 0x4d, 0x62, 0xfc, 0x73, 0xf0, 0x04, 0x01, 0x74,
	0xa6, 0x13, 0xfc, 0xe3, 0x44, 0xbb, 0x0c, 0xa0, 0x3b, 0x4d, 0x62, 0x3c, 0x3e, 0x99, 0x04, 0x6e,
	0xf4, 0xbb, 0x0b, 0xb0, 0x4e, 0x8d, 0x3e, 0x01, 0xbf, 0x52, 0x42, 0x12, 0x46, 0xd3, 0x6b, 0x52,
	0x50, 0xc3, 0x55, 0x1f, 0x0f, 0x6a, 0xdd, 0x39, 0x29, 0x28, 0xfa, 0x14, 0xb6, 0x24, 0xcd, 0x89,
	0xe2, 0x37, 0x34, 0x2d, 0x89, 0x5a, 0xd4, 0x34, 0xf8, 0x8d, 0xf2, 0x82, 0xa8, 0x05, 0x3a, 0x80,
	0x1d, 0xc6, 0x55, 0x2a, 0x66, 0xbf, 0xd2, 0xb9, 0x4a, 0x33, 0x2e, 0xe9, 0x5c, 0xc7, 0xaf, 0xdb,
	0x46, 0x8c, 0xab, 0xd8, 0x98, 0xbe, 0x6b, 0x2c, 0xe8, 0x04, 0x86, 0xda, 0x83, 0xe4, 0x8a, 0xca,
	0x6b, 0xa2, 0xe8, 0x7d, 0x5f, 0x4e, 0xab, 0xb0, 0x3d, 0x6c, 0xed, 0xf5, 0xf1, 0x33, 0xc6, 0xd5,
	0xb8, 0x81, 0xbd, 0x1b, 0x86, 0xd3, 0x4a, 0xd7, 0xc7, 0x36, 0x09, 0x0f, 0x3b, 0xb6, 0x3e, 0xb6,
	0x41, 0x31, 0xfa, 0x0c, 0xde, 0x63, 0x79, 0x5a, 0x4a, 0x61, 0x72, 0x98, 0x36, 0x7a, 0x06, 0xb6,
	0xc5, 0xf2, 0x0b, 0xab, 0xd5, 0x7d, 0xbc, 0xf6, 0x7a, 0x4e, 0xe0, 0xbe, 0xf6, 0x7a, 0xdd, 0xa0,
	0x87, 0x3d, 0x0d, 0x8b, 0xfe, 0x72, 0xa0, 0x7f, 0xc2, 0xd5, 0xb1, 0x28, 0x0a, 0xae, 0xd0, 0x36,
	0xb8, 0x3c, 0x33, 0xab, 0xd8, 0xc7, 0x2e, 0xcf, 0x50, 0x08, 0xdd, 0x6a, 0x69, 0x4a, 0x32, 0xd4,
	0xf9, 0xb8, 0x11, 0x11, 0x02, 0x6f, 0x26, 0xb2, 0x5b, 0xc3, 0x96, 0x8f, 0xcd, 0x1b, 0x7d, 0x01,
	0x1d, 0xb2, 0x54, 0x0b, 0x21, 0x0d, 0x2f, 0x83, 0xc3, 0x9d, 0x66, 0x27, 0x6d, 0xf4, 0xb1, 0xb1,
	0xe1, 0x1a, 0x83, 0x0e, 0xa1, 0x3f, 0x37, 0x7a, 0x45, 0x65, 0xd8, 0xfe, 0x0f, 0x87, 0x35, 0x0c,
	0x3d, 0x03, 0x28, 0x89, 0xa4, 0xd7, 0x2a, 0xe5, 0x59, 0x15, 0x76, 0x0c, 0x7f, 0x7d, 0xab, 0x39,
	0xcd, 0x2a, 0xf4, 0x14, 0xfa, 0xba, 0x90, 0xb4, 0xe2, 0x77, 0x34, 0xec, 0x0e, 0x9d, 0xbd, 0x16,
	0xee, 0x69, 0xc5, 0x94, 0xdf, 0xd1, 0xe8, 0x2d, 0xf8, 0x9b, 0x61, 0x75, 0x07, 0x66, 0x27, 0x1c,
	0xdb, 0x81, 0x7e, 0xa3, 0x1d, 0x68, 0xd3, 0x82, 0xf0, 0xbc, 0xee, 0xd6, 0x0a, 0x68, 0x04, 0x5e,
	0x46, 0x14, 0x35, 0xbd, 0x0e, 0xf4, 0xa5, 0x99, 0x13, 0x1f, 0x35, 0x27, 0x3e, 0x4a, 0x9a, 0x6f,
	0x00, 0x36, 0x38, 0xcd, 0xa2, 0xba, 0x33, 0x1c, 0xf8, 0xd8, 0x55, 0x77, 0x51, 0x04, 0x30, 0x79,
	0xcb, 0xd5, 0x54, 0x11, 0xb5, 0xac, 0x74, 0x8e, 0x1b, 0x92, 0x2f, 0x6d, 0xe2, 0x36, 0xb6, 0x42,
	0x94, 0x40, 0xe7, 0x48, 0x92, 0xeb, 0xf9, 0xe2, 0xd1, 0xba, 0x5e, 0xc1, 0x56, 0x7d, 0xb5, 0x96,
	0x0b, 0x53, 0xdf, 0xe0, 0xf0, 0xfd, 0x86, 0xaf, 0xd5, 0x04, 0xb1, 0x6f, 0x71, 0x56, 0x8a, 0xfe,
	0x70, 0xa0, 0x95, 0x10, 0xf6, 0x68, 0x4c, 0x3b, 0x6b, 0x77, 0x35, 0xeb, 0x07, 0x39, 0x5a, 0xff,
	0x2b, 0x87, 0xde, 0x91, 0x82, 0x56, 0x15, 0x61, 0xb4, 0x6e, 0xb9, 0x11, 0xf5, 0xf5, 0xd5, 0x4f,
	0x3b, 0x91, 0xb6, 0x99, 0xc8, 0xa0, 0xd6, 0xe9, 0xa1, 0xe8, 0x95, 0x51, 0x84, 0x31, 0x2a, 0xcd,
	0x5a, 0xff, 0xeb, 0xca, 0x58, 0x4c, 0x74, 0x05, 0xde, 0x65, 0x45, 0x25, 0xfa, 0x00, 0xda, 0x2c,
	0x4f, 0x57, 0x9b, 0xea, 0xb1, 0xfc, 0x34, 0x5b, 0xf5, 0xe8, 0x3e, 0x36, 0xcf, 0xd6, 0xe6, 0x3c,
	0x5f, 0xc0, 0x80, 0xe5, 0xe9, 0xb2, 0xd2, 0x27, 0x57, 0xd0, 0xfa, 0x88, 0x81, 0xe5, 0x97, 0xb5,
	0x26, 0xfa, 0x16, 0xc0, 0x1e, 0xe2, 0x85, 0x10, 0x39, 0x3a, 0x04, 0xd8, 0x38, 0x3f, 0xc7, 0xd4,
	0x89, 0x9a, 0x3a, 0xd7, 0x47, 0x88, 0x37, 0x50, 0x9f, 0x1f, 0x35, 0x11, 0x92, 0xdb, 0x92, 0xbe,
	0xfb, 0xbd, 0x03, 0xe8, 0x1c, 0xc7, 0x67, 0x67, 0xa7, 0x49, 0xe0, 0xa0, 0x1e, 0x78, 0x47, 0x6f,
	0xe2, 0xa3, 0xc0, 0xd5, 0xaf, 0x04, 0x4f, 0x26, 0x41, 0x0b, 0x75, 0xa1, 0x95, 0x8c, 0x4f, 0x02,
	0xef, 0xab, 0x18, 0xba, 0xa2, 0x4c, 0x95, 0x0e, 0xf0, 0xfc, 0xc1, 0xce, 0x9d, 0x51, 0xb5, 0x10,
	0x59, 0x5c, 0xea, 0xef, 0x69, 0x15, 0xfe, 0xfd, 0xdb, 0xbd, 0x03, 0xda, 0xfc, 0x0b, 0xe0, 0x8e,
	0x28, 0x75, 0x19, 0x47, 0x07, 0xbf, 0x68, 0x73, 0x4e, 0x66, 0xa3, 0xb9, 0x28, 0xf6, 0xed, 0xf3,
	0x4b, 0x21, 0xd9, 0xbe, 0x75, 0xb2, 0xff, 0xac, 0x7d, 0x26, 0x6a, 0xb9, 0x9c, 0xcd, 0x3a, 0x46,
	0xf5, 0xf2, 0x9f, 0x00, 0x00, 0x00, 0xff, 0xff, 0x36, 0x59, 0x9e, 0xa7, 0x0d, 0x07, 0x00, 0x00,
}
