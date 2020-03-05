package linter

import (
	"errors"
	"fmt"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	goplugin "github.com/golang/protobuf/protoc-gen-go/plugin"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/proto/go/internal"
)

// ensureMethodOpType will ensure that method includes the op_type option.
// See proto example below:
//
//  rpc ExampleMethod(ExampleMethodRequest) returns (ExampleMethodResponse) {
//     option (op_type).op = ACCESSOR;
//   }
func ensureMethodOpType(fileDesc *descriptor.FileDescriptorProto, m *descriptor.MethodDescriptorProto, req *goplugin.CodeGeneratorRequest) error {
	opMsg, err := internal.GetOpExtension(m)
	if err != nil {
		return err
	}

	ml := methodLinter{
		req:        req,
		fileDesc:   fileDesc,
		methodDesc: m,
		opMsg:      opMsg,
	}

	switch opCode := opMsg.GetOp(); opCode {

	case gitalypb.OperationMsg_ACCESSOR:
		return ml.validateAccessor()

	case gitalypb.OperationMsg_MUTATOR:
		// if mutator, we need to make sure we specify scope or target repo
		return ml.validateMutator()

	case gitalypb.OperationMsg_UNKNOWN:
		return errors.New("op set to UNKNOWN")

	default:
		return fmt.Errorf("invalid operation class with int32 value of %d", opCode)
	}
}

// LintFile ensures the file described meets Gitaly required processes.
// Currently, this is limited to validating if request messages contain
// a mandatory operation code.
func LintFile(file *descriptor.FileDescriptorProto, req *goplugin.CodeGeneratorRequest) []error {
	var errs []error

	for _, serviceDescriptorProto := range file.GetService() {
		for _, methodDescriptorProto := range serviceDescriptorProto.GetMethod() {
			err := ensureMethodOpType(file, methodDescriptorProto, req)
			if err != nil {
				// decorate error with current file and method
				err = fmt.Errorf(
					"%s: Method %q: %s",
					file.GetName(), methodDescriptorProto.GetName(), err,
				)
				errs = append(errs, err)
			}
		}
	}

	return errs
}
