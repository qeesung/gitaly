package praefect

import (
	"context"
	"crypto/sha1"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTransactionSucceeds(t *testing.T) {
	cc, _, cleanup := runPraefectServerWithGitaly(t, testConfig(1))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := gitalypb.NewRefTransactionClient(cc)

	registration, err := client.RegisterTransaction(ctx, &gitalypb.RegisterTransactionRequest{
		Nodes: []string{"node1"},
	})
	require.NoError(t, err)
	require.NotZero(t, registration.TransactionId)

	hash := sha1.Sum([]byte{})

	response, err := client.StartTransaction(ctx, &gitalypb.StartTransactionRequest{
		TransactionId:        registration.TransactionId,
		Node:                 "node1",
		ReferenceUpdatesHash: hash[:],
	})
	require.NoError(t, err)
	require.Equal(t, gitalypb.StartTransactionResponse_COMMIT, response.State)
}

func TestTransactionFailures(t *testing.T) {
	cc, _, cleanup := runPraefectServerWithGitaly(t, testConfig(1))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := gitalypb.NewRefTransactionClient(cc)

	hash := sha1.Sum([]byte{})
	_, err := client.StartTransaction(ctx, &gitalypb.StartTransactionRequest{
		TransactionId:        1,
		Node:                 "node1",
		ReferenceUpdatesHash: hash[:],
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestTransactionCancellation(t *testing.T) {
	cc, _, cleanup := runPraefectServerWithGitaly(t, testConfig(1))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := gitalypb.NewRefTransactionClient(cc)

	registration, err := client.RegisterTransaction(ctx, &gitalypb.RegisterTransactionRequest{
		Nodes: []string{"node1"},
	})
	require.NoError(t, err)
	require.NotZero(t, registration.TransactionId)

	_, err = client.CancelTransaction(ctx, &gitalypb.CancelTransactionRequest{
		TransactionId: registration.TransactionId,
	})
	require.NoError(t, err)

	hash := sha1.Sum([]byte{})
	_, err = client.StartTransaction(ctx, &gitalypb.StartTransactionRequest{
		TransactionId:        registration.TransactionId,
		Node:                 "node1",
		ReferenceUpdatesHash: hash[:],
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}
