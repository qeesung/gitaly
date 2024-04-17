package testhelper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb/testproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

// SetCtxGrpcMethod will set the gRPC context value for the proper key
// responsible for an RPC full method name. This directly corresponds to the
// gRPC function responsible for extracting the method:
// https://godoc.org/google.golang.org/grpc#Method
func SetCtxGrpcMethod(ctx context.Context, method string) context.Context {
	return grpc.NewContextWithServerTransportStream(ctx, mockServerTransportStream{method})
}

type mockServerTransportStream struct {
	method string
}

func (msts mockServerTransportStream) Method() string             { return msts.method }
func (mockServerTransportStream) SetHeader(md metadata.MD) error  { return nil }
func (mockServerTransportStream) SendHeader(md metadata.MD) error { return nil }
func (mockServerTransportStream) SetTrailer(md metadata.MD) error { return nil }

// ProtoEqual asserts that expected and actual protobuf messages are equal.
// It can accept not only proto.Message, but slices, maps, and structs too.
// This is required as comparing messages directly with `require.Equal` doesn't
// work.
func ProtoEqual(tb testing.TB, expected, actual interface{}, opts ...cmp.Option) {
	tb.Helper()

	ProtoEqualAssert(tb, expected, actual, opts...)
	if tb.Failed() {
		tb.FailNow()
	}
}

// ProtoEqualAssert is similar to ProtoEqual but safe to use in goroutines.
func ProtoEqualAssert(tb testing.TB, expected, actual interface{}, opts ...cmp.Option) {
	tb.Helper()

	opts = append(opts, protocmp.Transform(), cmpopts.EquateErrors())
	diff := cmp.Diff(expected, actual, opts...)
	if len(diff) > 0 {
		assert.Fail(tb, fmt.Sprintf("Protobufs not equal\nDiff: %v", diff))
	}
}

// RequireGrpcCode asserts that the error has the expected gRPC status code.
func RequireGrpcCode(tb testing.TB, err error, expectedCode codes.Code) {
	tb.Helper()

	require.Error(tb, err)
	status, ok := status.FromError(err)
	require.True(tb, ok)
	require.Equal(tb, expectedCode, status.Code())
}

// AssertGrpcCode asserts that the error has the expected gRPC status code.
func AssertGrpcCode(tb testing.TB, err error, expectedCode codes.Code) {
	tb.Helper()

	assert.Error(tb, err)
	status, ok := status.FromError(err)
	assert.True(tb, ok)
	assert.Equal(tb, expectedCode, status.Code())
}

// RequireGrpcError asserts that expected and actual gRPC errors are equal. Comparing gRPC errors
// directly with `require.Equal()` will not typically work correct.
func RequireGrpcError(tb testing.TB, expected, actual error, opts ...cmp.Option) {
	tb.Helper()
	// .Proto() handles nil receiver
	ProtoEqual(tb, status.Convert(expected).Proto(), status.Convert(actual).Proto(), opts...)
}

// RequireGrpcErrorContains performs the same assertion as RequireGrpcCode, but also asserts that
// the actual error message contains the expected error message as a substring.
func RequireGrpcErrorContains(tb testing.TB, expected, actual error) {
	tb.Helper()

	expectedConverted := status.Convert(expected).Proto()
	actualConverted := status.Convert(actual).Proto()

	require.Equal(tb, expectedConverted.Code, actualConverted.Code)

	require.Contains(tb, actualConverted.Message, expectedConverted.Message)
}

// RequireStatusWithErrorMetadataRegexp asserts that expected and actual error match each other. Both are expected to
// be status errors. The error metadata in the status is matched against regular expressions defined in expectedMetadata.
// expectedMetadata is keyed by error metadata key, and the value is a regex string that the value is expected to match.
//
// This method is useful when the error metadata contains values that should not be asserted for equality like changing paths.
// If the metadata is expected to be equal, use an equality assertion instead.
func RequireStatusWithErrorMetadataRegexp(tb testing.TB, expected, actual error, expectedMetadata map[string]string) {
	tb.Helper()

	actualStatus, ok := status.FromError(actual)
	require.True(tb, ok, "actual was not a status: %+v", actual)

	actualDetails := actualStatus.Details()

	actualWithoutMetadata := actualStatus.Proto()
	actualWithoutMetadata.Details = nil
	RequireGrpcError(tb, expected, status.ErrorProto(actualWithoutMetadata))

	actualKeys := make([]string, 0, len(actualDetails))
	for _, detail := range actualDetails {
		actualKeys = append(actualKeys, string(detail.(*testproto.ErrorMetadata).Key))
	}

	expectedKeys := make([]string, 0, len(expectedMetadata))
	for key := range expectedMetadata {
		expectedKeys = append(expectedKeys, key)
	}

	require.ElementsMatch(tb, expectedKeys, actualKeys, "actual metadata keys don't match expected")

	for _, detail := range actualDetails {
		metadata := detail.(*testproto.ErrorMetadata)
		require.Regexp(tb, expectedMetadata[string(metadata.Key)], string(metadata.Value), "metadata key %q's value didn't match expected", metadata.Key)
	}
}

// MergeOutgoingMetadata merges provided metadata-s and returns context with resulting value.
func MergeOutgoingMetadata(ctx context.Context, md ...metadata.MD) context.Context {
	ctxmd, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return metadata.NewOutgoingContext(ctx, metadata.Join(md...))
	}

	return metadata.NewOutgoingContext(ctx, metadata.Join(append(md, ctxmd)...))
}

// MergeIncomingMetadata merges provided metadata-s and returns context with resulting value.
func MergeIncomingMetadata(ctx context.Context, md ...metadata.MD) context.Context {
	ctxmd, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return metadata.NewIncomingContext(ctx, metadata.Join(md...))
	}

	return metadata.NewIncomingContext(ctx, metadata.Join(append(md, ctxmd)...))
}

// WithInterceptedMetadata adds an additional metadata item to the Error in the form of an error
// detail. Note that this is only intended to be used in the context of tests where we convert error
// metadata into structured errors via the StructErrUnaryInterceptor and StructErrStreamInterceptor so that we can
// test that metadata has been set as expected on the client-side of a gRPC call.
func WithInterceptedMetadata(err structerr.Error, key string, value any) structerr.Error {
	if key == "relative_path" {
		// There are a number of tests that assert the returned error metadata for equality.
		// The relative path might be rewritten before the handler which leads to the equality
		// checks failing. Override the returned relative path so the actual values are not asserted,
		// just that the key is present.
		//
		// This is not really an ideal fix as this overrides a value magically. To remove this, we'll
		// have to adjust every test that is asserting the relative paths to not do so.
		value = "OVERRIDDEN_BY_TESTHELPER"
	}

	return err.WithDetail(&testproto.ErrorMetadata{
		Key:   []byte(key),
		Value: []byte(fmt.Sprintf("%v", value)),
	})
}

// WithInterceptedMetadataItems adds multiple metadata items to the Error. It behaves as if
// WithInterceptedMetadata was called with each of the key-value items separately.
func WithInterceptedMetadataItems(err structerr.Error, items ...structerr.MetadataItem) structerr.Error {
	for _, item := range items {
		err = WithInterceptedMetadata(err, item.Key, item.Value)
	}
	return err
}

// ToInterceptedMetadata converts error metadata to intercepted error details.
func ToInterceptedMetadata(err structerr.Error) structerr.Error {
	return WithInterceptedMetadataItems(err, err.MetadataItems()...)
}

// Receive receives all responses from the receiver until an error is encountered or `io.EOF` is received.
func Receive[Response any](receiver func() (Response, error)) ([]Response, error) {
	var responses []Response
	for {
		response, err := receiver()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}

			return responses, err
		}

		responses = append(responses, response)
	}
}

// ReceiveAndFold receives all responses from the receiver and then folds the results with the given folder. The folder
// will be called even in the case where the receiver returns an error.
func ReceiveAndFold[Response, Result any](receiver func() (Response, error), folder func(initial Result, response Response) Result) (Result, error) {
	responses, err := Receive(receiver)

	var result Result
	for _, response := range responses {
		result = folder(result, response)
	}

	return result, err
}
