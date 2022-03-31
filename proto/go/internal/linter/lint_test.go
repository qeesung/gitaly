package linter

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	_ "gitlab.com/gitlab-org/gitaly/v14/proto/go/internal/linter/testdata"
	"google.golang.org/protobuf/reflect/protodesc"
	protoreg "google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
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
				formatError("go/internal/linter/testdata/invalid.proto", "InterceptedWithOperationType", "InvalidMethod", errors.New("operation type defined on an intercepted method")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod0", errors.New("missing op_type extension")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod1", errors.New("op set to UNKNOWN")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod2", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod4", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod5", errors.New("wrong type of field RequestWithWrongTypeRepository.header.repository, expected .gitaly.Repository, got .test.InvalidMethodResponse")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod6", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod7", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod8", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod9", errors.New("unexpected count of target_repository fields 1, expected 0, found target_repository label at: [InvalidMethodRequestWithRepo.destination]")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod10", errors.New("unexpected count of storage field 1, expected 0, found storage label at: [RequestWithStorageAndRepo.storage_name]")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod11", errors.New("unexpected count of storage field 1, expected 0, found storage label at: [RequestWithNestedStorageAndRepo.inner_message.storage_name]")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod13", errors.New("unexpected count of storage field 0, expected 1, found storage label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod14", errors.New("unexpected count of storage field 2, expected 1, found storage label at: [RequestWithMultipleNestedStorage.inner_message.storage_name RequestWithMultipleNestedStorage.storage_name]")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "InvalidMethod15", errors.New("operation type defined on an intercepted method")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "MaintenanceWithMissingRepository", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "MaintenanceWithUnflaggedRepository", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "MaintenanceWithWrongNestedRepositoryType", errors.New("wrong type of field RequestWithWrongTypeRepository.header.repository, expected .gitaly.Repository, got .test.InvalidMethodResponse")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "MaintenanceWithInvalidTargetType", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "MaintenanceWithInvalidNestedRequest", errors.New("unexpected count of target_repository fields 0, expected 1, found target_repository label at: []")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "MaintenanceWithStorageAndRepository", errors.New("unexpected count of storage field 1, expected 0, found storage label at: [RequestWithStorageAndRepo.storage_name]")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "MaintenanceWithNestedStorageAndRepository", errors.New("unexpected count of storage field 1, expected 0, found storage label at: [RequestWithNestedStorageAndRepo.inner_message.storage_name]")),
				formatError("go/internal/linter/testdata/invalid.proto", "InvalidService", "MaintenanceWithStorageScope", errors.New("unknown operation scope level 2")),
			},
		},
	} {
		t.Run(tt.protoPath, func(t *testing.T) {
			fd, err := protoreg.GlobalFiles.FindFileByPath(tt.protoPath)
			require.NoError(t, err)

			fdToCheck := protodesc.ToFileDescriptorProto(fd)
			req := &pluginpb.CodeGeneratorRequest{
				ProtoFile: []*descriptorpb.FileDescriptorProto{fdToCheck},
			}

			for _, protoPath := range []string{
				// as we have no input stream we can use to create CodeGeneratorRequest
				// we must create it by hands with all required dependencies loaded
				"google/protobuf/descriptor.proto",
				"google/protobuf/timestamp.proto",
				"lint.proto",
				"shared.proto",
			} {
				fd, err := protoreg.GlobalFiles.FindFileByPath(protoPath)
				require.NoError(t, err)
				req.ProtoFile = append(req.ProtoFile, protodesc.ToFileDescriptorProto(fd))
			}

			errs := LintFile(fdToCheck, req)
			require.Equal(t, tt.errs, errs)
		})
	}
}
