package internal

import (
	"errors"
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// GetOpExtension gets the OperationMsg from a method descriptor
func GetOpExtension(m *descriptor.MethodDescriptorProto) (*gitalypb.OperationMsg, error) {
	options := m.GetOptions()

	if !proto.HasExtension(options, gitalypb.E_OpType) {
		return nil, errors.New("missing op_type option")
	}

	ext, err := proto.GetExtension(options, gitalypb.E_OpType)
	if err != nil {
		return nil, err
	}

	opMsg, ok := ext.(*gitalypb.OperationMsg)
	if !ok {
		return nil, fmt.Errorf("unable to obtain OperationMsg from %#v", ext)
	}

	return opMsg, nil
}
