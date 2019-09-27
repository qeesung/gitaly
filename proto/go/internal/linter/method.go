package linter

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/proto/go/internal"
)

type methodLinter struct {
	fileDesc   *descriptor.FileDescriptorProto
	methodDesc *descriptor.MethodDescriptorProto
	opMsg      *gitalypb.OperationMsg
}

// validateAccessor will ensure the accessor method does not specify a target
// repo
func (ml methodLinter) validateAccessor() error {
	switch ml.opMsg.GetScopeLevel() {
	case gitalypb.OperationMsg_REPOSITORY:
		return ml.ensureValidRepoScope()
	case gitalypb.OperationMsg_STORAGE:
		return ml.ensureValidStorageScope()
	}

	return nil
}

// validateMutator will ensure the following rules:
//  - Mutator RPC's with repository level scope must specify a target repo
//  - Mutator RPC's without target repo must not be scoped at repo level
func (ml methodLinter) validateMutator() error {
	switch scope := ml.opMsg.GetScopeLevel(); scope {

	case gitalypb.OperationMsg_REPOSITORY:
		return ml.ensureValidRepoScope()

	case gitalypb.OperationMsg_SERVER:
		return ml.ensureValidServerScope()

	case gitalypb.OperationMsg_STORAGE:
		return ml.ensureValidStorageScope()

	default:
		return fmt.Errorf("unknown operation scope level %d", scope)

	}
}

// TODO: add checks for storage location via valid field annotation for Gitaly HA
func (ml methodLinter) ensureValidStorageScope() error {
	if ml.opMsg.GetTargetRepositoryField() != "" {
		return errors.New("storage level scoped RPC should not specify target repo")
	}
	return nil
}

func (ml methodLinter) ensureValidServerScope() error {
	if ml.opMsg.GetTargetRepositoryField() != "" {
		return errors.New("server level scoped RPC should not specify target repo")
	}
	return nil
}

func (ml methodLinter) ensureValidRepoScope() error {
	return ml.ensureValidTargetRepo()
}

const repoTypeName = ".gitaly.Repository"

func (ml methodLinter) ensureValidTargetRepo() error {
	if ml.opMsg.GetTargetRepositoryField() == "" {
		return errors.New("missing target repository field")
	}

	oids, err := parseOID(ml.opMsg.GetTargetRepositoryField())
	if err != nil {
		return err
	}

	sharedMsgs, err := getSharedTypes()
	if err != nil {
		return err
	}

	topLevelMsgs := map[string]*descriptor.DescriptorProto{}
	for _, msg := range append(ml.fileDesc.GetMessageType(), sharedMsgs...) {
		topLevelMsgs[msg.GetName()] = msg
	}

	reqMsgName, err := lastName(ml.methodDesc.GetInputType())
	if err != nil {
		return err
	}

	msgT := topLevelMsgs[reqMsgName]
	targetType := ""
	visited := 0

	// TODO: Improve code quality by removing OID_FIELDS and MSG_FIELDS labels
OID_FIELDS:
	for i, fieldNo := range oids {
		fields := msgT.GetField()
	MSG_FIELDS:
		for _, f := range fields {
			if f.GetNumber() == int32(fieldNo) {
				visited++

				targetType = f.GetTypeName()
				if targetType == "" {
					// primitives like int32 don't have TypeName
					targetType = f.GetType().String()
				}

				// if last OID, we're done
				if i == len(oids)-1 {
					break OID_FIELDS
				}

				// if not last OID, descend into next nested message
				const msgPrimitive = "TYPE_MESSAGE"
				if primitive := f.GetType().String(); primitive != msgPrimitive {
					return fmt.Errorf(
						"expected %d-th field of OID %+v to be %s, but got %s",
						i+1, oids, msgPrimitive, primitive,
					)
				}

				msgName, err := lastName(f.GetTypeName())
				if err != nil {
					return err
				}

				// first check if field refers to a nested type
				for _, nestedType := range msgT.GetNestedType() {
					if msgName == nestedType.GetName() {
						msgT = nestedType
						break MSG_FIELDS
					}
				}

				// then, check if field refers to a top level type
				var ok bool
				msgT, ok = topLevelMsgs[msgName]
				if !ok {
					return fmt.Errorf(
						"could not find message type %q for %d-th element %d of target OID %+v",
						msgName, i+1, fieldNo, oids,
					)
				}
				break
			}
		}
	}

	if visited != len(oids) {
		return fmt.Errorf("target repo OID %+v does not exist in request message", oids)
	}

	if targetType != repoTypeName {
		return fmt.Errorf(
			"unexpected type %s (expected %s) for target repo field addressed by %+v",
			targetType, repoTypeName, oids,
		)
	}

	return nil
}

func lastName(inputType string) (string, error) {
	tokens := strings.Split(inputType, ".")

	msgName := tokens[len(tokens)-1]
	if msgName == "" {
		return "", fmt.Errorf("unable to parse method input type: %s", inputType)
	}

	return msgName, nil
}

// parses a string like "1.1" and returns a slice of ints
func parseOID(rawFieldUID string) ([]int, error) {
	fieldNoStrs := strings.Split(rawFieldUID, ".")

	if len(fieldNoStrs) < 1 {
		return nil,
			fmt.Errorf("OID string contains no field numbers: %s", fieldNoStrs)
	}

	fieldNos := make([]int, len(fieldNoStrs))

	for i, fieldNoStr := range fieldNoStrs {
		fieldNo, err := strconv.Atoi(fieldNoStr)
		if err != nil {
			return nil,
				fmt.Errorf("unable to parse target field OID %s: %s", rawFieldUID, err)
		}
		if fieldNo < 1 {
			return nil, errors.New("zero is an invalid field number")
		}
		fieldNos[i] = fieldNo
	}

	return fieldNos, nil
}

func getSharedTypes() ([]*descriptor.DescriptorProto, error) {
	sharedFD, err := internal.ExtractFile(proto.FileDescriptor("shared.proto"))
	if err != nil {
		return nil, err
	}

	return sharedFD.GetMessageType(), nil
}
