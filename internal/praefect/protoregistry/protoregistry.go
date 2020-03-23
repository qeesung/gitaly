package protoregistry

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"reflect"
	"strings"
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// GitalyProtoFileDescriptors is a slice of all gitaly registered file descriptors
var (
	// GitalyProtoFileDescriptors is a slice of all gitaly registered file
	// descriptors
	GitalyProtoFileDescriptors []*descriptor.FileDescriptorProto
	// GitalyProtoPreregistered is a proto registry pre-registered with all
	// GitalyProtoFileDescriptors file descriptor protos
	GitalyProtoPreregistered *Registry
)

func init() {
	for _, protoName := range gitalypb.GitalyProtos {
		gz := proto.FileDescriptor(protoName)
		fd, err := ExtractFileDescriptor(gz)
		if err != nil {
			panic(err)
		}

		GitalyProtoFileDescriptors = append(GitalyProtoFileDescriptors, fd)
	}

	GitalyProtoPreregistered = New()
	if err := GitalyProtoPreregistered.RegisterFiles(GitalyProtoFileDescriptors...); err != nil {
		panic(err)
	}
}

// OpType represents the operation type for a RPC method
type OpType int

const (
	// OpUnknown = unknown operation type
	OpUnknown = iota
	// OpAccessor = accessor operation type (ready only)
	OpAccessor
	// OpMutator = mutator operation type (modifies a repository)
	OpMutator
)

// Scope represents the intended scope of an RPC method
type Scope int

const (
	// ScopeUnknown is the default scope until determined otherwise
	ScopeUnknown = iota
	// ScopeRepository indicates an RPC's scope is limited to a repository
	ScopeRepository = iota
	// ScopeStorage indicates an RPC is scoped to an entire storage location
	ScopeStorage
	// ScopeServer indicates an RPC is scoped to an entire server
	ScopeServer
)

var protoScope = map[gitalypb.OperationMsg_Scope]Scope{
	gitalypb.OperationMsg_SERVER:     ScopeServer,
	gitalypb.OperationMsg_REPOSITORY: ScopeRepository,
	gitalypb.OperationMsg_STORAGE:    ScopeStorage,
}

// MethodInfo contains metadata about the RPC method. Refer to documentation
// for message type "OperationMsg" shared.proto in ./proto for
// more documentation.
type MethodInfo struct {
	Operation      OpType
	Scope          Scope
	targetRepo     []int
	additionalRepo []int
	requestName    string // protobuf message name for input type
	requestFactory protoFactory
	storage        []int
}

// TargetRepo returns the target repository for a protobuf message if it exists
func (mi MethodInfo) TargetRepo(msg proto.Message) (*gitalypb.Repository, error) {
	return mi.getRepo(msg, mi.targetRepo)
}

// AdditionalRepo returns the additional repository for a protobuf message that needs a storage rewritten
// if it exists
func (mi MethodInfo) AdditionalRepo(msg proto.Message) (*gitalypb.Repository, bool, error) {
	if mi.additionalRepo == nil {
		return nil, false, nil
	}

	repo, err := mi.getRepo(msg, mi.additionalRepo)

	return repo, true, err
}

func (mi MethodInfo) getRepo(msg proto.Message, targetOid []int) (*gitalypb.Repository, error) {
	if mi.requestName != proto.MessageName(msg) {
		return nil, fmt.Errorf(
			"proto message %s does not match expected RPC request message %s",
			proto.MessageName(msg), mi.requestName,
		)
	}

	repo, err := reflectFindRepoTarget(msg, targetOid)
	switch {
	case err != nil:
		return nil, err
	case repo == nil:
		// it is possible for the target repo to not be set (especially in our unit
		// tests designed to fail and this should return an error to prevent nil
		// pointer dereferencing
		return nil, ErrTargetRepoMissing
	default:
		return repo, nil
	}
}

// Storage returns the storage name for a protobuf message if it exists
func (mi MethodInfo) Storage(msg proto.Message) (string, error) {
	if mi.requestName != proto.MessageName(msg) {
		return "", fmt.Errorf(
			"proto message %s does not match expected RPC request message %s",
			proto.MessageName(msg), mi.requestName,
		)
	}

	return reflectFindStorage(msg, mi.storage)
}

// UnmarshalRequestProto will unmarshal the bytes into the method's request
// message type
func (mi MethodInfo) UnmarshalRequestProto(b []byte) (proto.Message, error) {
	return mi.requestFactory(b)
}

// Registry contains info about RPC methods
type Registry struct {
	sync.RWMutex
	protos map[string]MethodInfo
}

// New creates a new ProtoRegistry
func New() *Registry {
	return &Registry{
		protos: make(map[string]MethodInfo),
	}
}

// RegisterFiles takes one or more descriptor.FileDescriptorProto and populates
// the registry with its info
func (pr *Registry) RegisterFiles(protos ...*descriptor.FileDescriptorProto) error {
	pr.Lock()
	defer pr.Unlock()

	for _, p := range protos {
		for _, svc := range p.GetService() {
			for _, method := range svc.GetMethod() {
				mi, err := parseMethodInfo(p, method)
				if err != nil {
					return err
				}

				fullMethodName := fmt.Sprintf(
					"/%s.%s/%s",
					p.GetPackage(), svc.GetName(), method.GetName(),
				)
				pr.protos[fullMethodName] = mi
			}
		}
	}

	return nil
}

func getOpExtension(m *descriptor.MethodDescriptorProto) (*gitalypb.OperationMsg, error) {
	options := m.GetOptions()

	if !proto.HasExtension(options, gitalypb.E_OpType) {
		return nil, fmt.Errorf("method %s missing op_type option", m.GetName())
	}

	ext, err := proto.GetExtension(options, gitalypb.E_OpType)
	if err != nil {
		return nil, fmt.Errorf("unable to get Gitaly custom OpType extension: %s", err)
	}

	opMsg, ok := ext.(*gitalypb.OperationMsg)
	if !ok {
		return nil, fmt.Errorf("unable to obtain OperationMsg from %#v", ext)
	}
	return opMsg, nil
}

type protoFactory func([]byte) (proto.Message, error)

func methodReqFactory(method *descriptor.MethodDescriptorProto) (protoFactory, error) {
	// for some reason, the descriptor prepends a dot not expected in Go
	inputTypeName := strings.TrimPrefix(method.GetInputType(), ".")

	inputType := proto.MessageType(inputTypeName)
	if inputType == nil {
		return nil, fmt.Errorf("no message type found for %s", inputType)
	}

	f := func(buf []byte) (proto.Message, error) {
		v := reflect.New(inputType.Elem())
		pb, ok := v.Interface().(proto.Message)
		if !ok {
			return nil, fmt.Errorf("factory function expected protobuf message but got %T", v.Interface())
		}

		if err := proto.Unmarshal(buf, pb); err != nil {
			return nil, err
		}

		return pb, nil
	}

	return f, nil
}

func parseMethodInfo(p *descriptor.FileDescriptorProto, methodDesc *descriptor.MethodDescriptorProto) (MethodInfo, error) {
	opMsg, err := getOpExtension(methodDesc)
	if err != nil {
		return MethodInfo{}, err
	}

	var opCode OpType

	switch opMsg.GetOp() {
	case gitalypb.OperationMsg_ACCESSOR:
		opCode = OpAccessor
	case gitalypb.OperationMsg_MUTATOR:
		opCode = OpMutator
	default:
		opCode = OpUnknown
	}

	// for some reason, the protobuf descriptor contains an extra dot in front
	// of the request name that the generated code does not. This trimming keeps
	// the two copies consistent for comparisons.
	requestName := strings.TrimLeft(methodDesc.GetInputType(), ".")

	reqFactory, err := methodReqFactory(methodDesc)
	if err != nil {
		return MethodInfo{}, err
	}

	scope, ok := protoScope[opMsg.GetScopeLevel()]
	if !ok {
		return MethodInfo{}, fmt.Errorf("encountered unknown method scope %d", opMsg.GetScopeLevel())
	}

	mi := MethodInfo{
		Operation:      opCode,
		Scope:          scope,
		requestName:    requestName,
		requestFactory: reqFactory,
	}

	topLevelMsgs, err := getTopLevelMsgs(p)
	if err != nil {
		return MethodInfo{}, err
	}

	typeName, err := lastName(methodDesc.GetInputType())
	if err != nil {
		return MethodInfo{}, err
	}

	if scope == ScopeRepository {
		m := matcher{
			match:        getTargetRepositoryExtension,
			subMatch:     getRepositoryExtension,
			expectedType: ".gitaly.Repository",
			topLevelMsgs: topLevelMsgs,
		}

		targetRepo, err := m.findField(topLevelMsgs[typeName])
		if err != nil {
			return MethodInfo{}, err
		}
		if targetRepo == nil {
			return MethodInfo{}, fmt.Errorf("unable to find target repository for method: %s", requestName)
		}
		mi.targetRepo = targetRepo

		m.match = getAdditionalRepositoryExtension
		additionalRepo, err := m.findField(topLevelMsgs[typeName])
		if err != nil {
			return MethodInfo{}, err
		}
		mi.additionalRepo = additionalRepo
	} else if scope == ScopeStorage {
		m := matcher{
			match:        getStorageExtension,
			topLevelMsgs: topLevelMsgs,
		}
		storage, err := m.findField(topLevelMsgs[typeName])
		if err != nil {
			return MethodInfo{}, err
		}
		if storage == nil {
			return MethodInfo{}, fmt.Errorf("unable to find storage for method: %s", requestName)
		}
		mi.storage = storage
	}

	return mi, nil
}

func getFileTypes(filename string) ([]*descriptor.DescriptorProto, error) {
	sharedFD, err := ExtractFileDescriptor(proto.FileDescriptor(filename))
	if err != nil {
		return nil, err
	}

	types := sharedFD.GetMessageType()

	for _, dep := range sharedFD.Dependency {
		depTypes, err := getFileTypes(dep)
		if err != nil {
			return nil, err
		}
		types = append(types, depTypes...)
	}

	return types, nil
}

func getTopLevelMsgs(p *descriptor.FileDescriptorProto) (map[string]*descriptor.DescriptorProto, error) {
	topLevelMsgs := map[string]*descriptor.DescriptorProto{}
	types, err := getFileTypes(p.GetName())
	if err != nil {
		return nil, err
	}
	for _, msg := range types {
		topLevelMsgs[msg.GetName()] = msg
	}
	return topLevelMsgs, nil
}

func getStorageExtension(m *descriptor.FieldDescriptorProto) (bool, error) {
	return getBoolExtension(m, gitalypb.E_Storage)
}

func getTargetRepositoryExtension(m *descriptor.FieldDescriptorProto) (bool, error) {
	return getBoolExtension(m, gitalypb.E_TargetRepository)
}

func getAdditionalRepositoryExtension(m *descriptor.FieldDescriptorProto) (bool, error) {
	return getBoolExtension(m, gitalypb.E_AdditionalRepository)
}

func getRepositoryExtension(m *descriptor.FieldDescriptorProto) (bool, error) {
	return getBoolExtension(m, gitalypb.E_Repository)
}

func getBoolExtension(m *descriptor.FieldDescriptorProto, extension *proto.ExtensionDesc) (bool, error) {
	options := m.GetOptions()

	if !proto.HasExtension(options, extension) {
		return false, nil
	}

	ext, err := proto.GetExtension(options, extension)
	if err != nil {
		return false, err
	}

	storageMsg, ok := ext.(*bool)
	if !ok {
		return false, fmt.Errorf("unable to obtain bool from %#v", ext)
	}

	if storageMsg == nil {
		return false, nil
	}

	return *storageMsg, nil
}

// Matcher helps find field matching credentials. At first match method is used to check fields
// recursively. Then if field matches but type don't match expectedType subMatch method is used
// from this point. This matcher assumes that only one field in the message matches the credentials.
type matcher struct {
	match        func(*descriptor.FieldDescriptorProto) (bool, error)
	subMatch     func(*descriptor.FieldDescriptorProto) (bool, error)
	expectedType string                                 // fully qualified name of expected type e.g. ".gitaly.Repository"
	topLevelMsgs map[string]*descriptor.DescriptorProto // Map of all top level messages in given file and it dependencies. Result of getTopLevelMsgs should be used.
}

func (m matcher) findField(t *descriptor.DescriptorProto) ([]int, error) {
	for _, f := range t.GetField() {
		match, err := m.match(f)
		if err != nil {
			return nil, err
		}
		if match {
			if f.GetTypeName() == m.expectedType {
				return []int{int(f.GetNumber())}, nil
			} else if m.subMatch != nil {
				m.match = m.subMatch
				m.subMatch = nil
			} else {
				return nil, fmt.Errorf("found wrong type, expected: %s, got: %s", m.expectedType, f.GetTypeName())
			}
		}

		childMsg, err := findChildMsg(m.topLevelMsgs, t, f)
		if err != nil {
			return nil, err
		}

		if childMsg != nil {
			nestedField, err := m.findField(childMsg)
			if err != nil {
				return nil, err
			}
			if nestedField != nil {
				return append([]int{int(f.GetNumber())}, nestedField...), nil
			}
		}
	}
	return nil, nil
}

func findChildMsg(topLevelMsgs map[string]*descriptor.DescriptorProto, t *descriptor.DescriptorProto, f *descriptor.FieldDescriptorProto) (*descriptor.DescriptorProto, error) {
	var childType *descriptor.DescriptorProto
	const msgPrimitive = "TYPE_MESSAGE"
	if primitive := f.GetType().String(); primitive != msgPrimitive {
		return nil, nil
	}

	msgName, err := lastName(f.GetTypeName())
	if err != nil {
		return nil, err
	}

	for _, nestedType := range t.GetNestedType() {
		if msgName == nestedType.GetName() {
			return nestedType, nil
		}
	}

	if childType = topLevelMsgs[msgName]; childType != nil {
		return childType, nil
	}

	return nil, fmt.Errorf("could not find message type %q", msgName)
}

func lastName(inputType string) (string, error) {
	tokens := strings.Split(inputType, ".")

	msgName := tokens[len(tokens)-1]
	if msgName == "" {
		return "", fmt.Errorf("unable to parse method input type: %s", inputType)
	}

	return msgName, nil
}

// LookupMethod looks up an MethodInfo by service and method name
func (pr *Registry) LookupMethod(fullMethodName string) (MethodInfo, error) {
	pr.RLock()
	defer pr.RUnlock()

	methodInfo, ok := pr.protos[fullMethodName]
	if !ok {
		return MethodInfo{}, fmt.Errorf("full method name not found: %v", fullMethodName)
	}
	return methodInfo, nil
}

// ExtractFileDescriptor extracts a FileDescriptorProto from a gzip'd buffer.
// https://github.com/golang/protobuf/blob/9eb2c01ac278a5d89ce4b2be68fe4500955d8179/descriptor/descriptor.go#L50
func ExtractFileDescriptor(gz []byte) (*descriptor.FileDescriptorProto, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip reader: %v", err)
	}
	defer r.Close()

	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to uncompress descriptor: %v", err)
	}

	fd := &descriptor.FileDescriptorProto{}
	if err := proto.Unmarshal(b, fd); err != nil {
		return nil, fmt.Errorf("malformed FileDescriptorProto: %v", err)
	}

	return fd, nil
}
