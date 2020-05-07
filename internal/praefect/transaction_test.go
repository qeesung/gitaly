package praefect

import (
	"context"
	"crypto/sha1"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func runPraefectWithTransactionMgr(t *testing.T) (*grpc.ClientConn, *transactions.Manager, testhelper.Cleanup) {
	conf := testConfig(1)

	ds := datastore.Datastore{
		ReplicasDatastore:     datastore.NewInMemory(conf),
		ReplicationEventQueue: datastore.NewMemoryReplicationEventQueue(),
	}

	txMgr := transactions.NewManager()
	conn, _, cleanup := runPraefectServer(t, conf, ds, txMgr)

	return conn, txMgr, cleanup
}

func TestTransactionSucceeds(t *testing.T) {
	cc, txMgr, cleanup := runPraefectWithTransactionMgr(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := gitalypb.NewRefTransactionClient(cc)

	transactionID, cancelTransaction, err := txMgr.RegisterTransaction(ctx, []string{"node1"})
	require.NoError(t, err)
	require.NotZero(t, transactionID)
	defer cancelTransaction()

	hash := sha1.Sum([]byte{})

	response, err := client.StartTransaction(ctx, &gitalypb.StartTransactionRequest{
		TransactionId:        transactionID,
		Node:                 "node1",
		ReferenceUpdatesHash: hash[:],
	})
	require.NoError(t, err)
	require.Equal(t, gitalypb.StartTransactionResponse_COMMIT, response.State)
}

func TestTransactionFailsWithMultipleNodes(t *testing.T) {
	_, txMgr, cleanup := runPraefectWithTransactionMgr(t)
	defer cleanup()

	ctx, cleanup := testhelper.Context()
	defer cleanup()

	_, _, err := txMgr.RegisterTransaction(ctx, []string{"node1", "node2"})
	require.Error(t, err)
}

func TestTransactionFailures(t *testing.T) {
	cc, _, cleanup := runPraefectWithTransactionMgr(t)
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
	cc, txMgr, cleanup := runPraefectWithTransactionMgr(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := gitalypb.NewRefTransactionClient(cc)

	transactionID, cancelTransaction, err := txMgr.RegisterTransaction(ctx, []string{"node1"})
	require.NoError(t, err)
	require.NotZero(t, transactionID)

	cancelTransaction()

	hash := sha1.Sum([]byte{})
	_, err = client.StartTransaction(ctx, &gitalypb.StartTransactionRequest{
		TransactionId:        transactionID,
		Node:                 "node1",
		ReferenceUpdatesHash: hash[:],
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}
