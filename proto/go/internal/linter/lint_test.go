package linter_test

import (
	"errors"
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	goplugin "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitaly/proto/go/internal"
	"gitlab.com/gitlab-org/gitaly/proto/go/internal/linter"
	_ "gitlab.com/gitlab-org/gitaly/proto/go/internal/linter/testdata"
)

func TestLintFile(t *testing.T) {
	for _, tt := range []struct {
		protoPath string
		errs      []error
	}{
		{
			protoPath: "go/internal/linter/testdata/valid.proto",
			errs:      nil,
		},
		{
			protoPath: "go/internal/linter/testdata/invalid.proto",
			errs: []error{
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod0": missing op_type option`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod1": op set to UNKNOWN`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod2": unexpected count of target_repository fields 0, expected 1, found target_repository label at: []`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod3": unexpected count of target_repository fields 1, expected 0, found target_repository label at: [InvalidMethodRequestWithRepo.destination]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod4": unexpected count of target_repository fields 0, expected 1, found target_repository label at: []`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod5": wrong type of field RequestWithWrongTypeRepository.header.repository, expected .gitaly.Repository, got .test.InvalidMethodResponse`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod6": unexpected count of target_repository fields 0, expected 1, found target_repository label at: []`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod7": unexpected count of target_repository fields 0, expected 1, found target_repository label at: []`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod8": unexpected count of target_repository fields 0, expected 1, found target_repository label at: []`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod9": unexpected count of target_repository fields 1, expected 0, found target_repository label at: [InvalidMethodRequestWithRepo.destination]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod10": unexpected count of storage field 1, expected 0, found storage label at: [RequestWithStorageAndRepo.storage_name]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod11": unexpected count of storage field 1, expected 0, found storage label at: [RequestWithNestedStorageAndRepo.inner_message.storage_name]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod12": unexpected count of storage field 1, expected 0, found storage label at: [RequestWithInnerNestedStorage.header.storage_name]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod13": unexpected count of storage field 0, expected 1, found storage label at: []`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod14": unexpected count of storage field 2, expected 1, found storage label at: [RequestWithMultipleNestedStorage.inner_message.storage_name RequestWithMultipleNestedStorage.storage_name]`),
			},
		},
	} {
		t.Run(tt.protoPath, func(t *testing.T) {
			fdToCheck, err := internal.ExtractFile(proto.FileDescriptor(tt.protoPath))
			require.NoError(t, err)

			req := &goplugin.CodeGeneratorRequest{
				ProtoFile: []*descriptor.FileDescriptorProto{fdToCheck},
			}

			for _, protoPath := range []string{
				// as we have no input stream we can use to create CodeGeneratorRequest
				// we must create it by hands with all required dependencies loaded
				"lint.proto",
				"shared.proto",
				"google/protobuf/descriptor.proto",
				"google/protobuf/timestamp.proto",
			} {
				fd, err := internal.ExtractFile(proto.FileDescriptor(protoPath))
				require.NoError(t, err)
				req.ProtoFile = append(req.ProtoFile, fd)
			}

			errs := linter.LintFile(fdToCheck, req)
			require.Equal(t, tt.errs, errs)
		})
	}
}
