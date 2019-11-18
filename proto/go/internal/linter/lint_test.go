package linter_test

import (
	"errors"
	"testing"

	"github.com/golang/protobuf/proto"
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
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod2": missing target repository field`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod3": server level scoped RPC should not specify target repo`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod4": missing target repository field`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod5": unable to parse target field OID 🐛: strconv.Atoi: parsing "🐛": invalid syntax`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod6": target repo OID [1] does not exist in request message`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod7": unexpected type TYPE_INT32 (expected .gitaly.Repository) for target repo field addressed by [1]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod8": expected 1-th field of OID [1 1] to be TYPE_MESSAGE, but got TYPE_INT32`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod9": target repo OID [1 2] does not exist in request message`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod10": unexpected type TYPE_INT32 (expected .gitaly.Repository) for target repo field addressed by [1 1]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod11": storage level scoped RPC should not specify target repo`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod12": unexpected count of storage field 1, expected 0, found storage label at: [RequestWithStorage.storage_name]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod13": unexpected count of storage field 1, expected 0, found storage label at: [RequestWithNestedStorage.inner_message.storage_name]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod14": unexpected count of storage field 1, expected 0, found storage label at: [RequestWithInnerNestedStorage.header.storage_name]`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod15": unexpected count of storage field 0, expected 1, found storage label at: []`),
				errors.New(`go/internal/linter/testdata/invalid.proto: Method "InvalidMethod16": unexpected count of storage field 2, expected 1, found storage label at: [RequestWithMultipleNestedStorage.inner_message.storage_name RequestWithMultipleNestedStorage.storage_name]`),
			},
		},
	} {
		t.Run(tt.protoPath, func(t *testing.T) {
			fd, err := internal.ExtractFile(proto.FileDescriptor(tt.protoPath))
			require.NoError(t, err)

			errs := linter.LintFile(fd)
			require.Equal(t, tt.errs, errs)
		})
	}
}
