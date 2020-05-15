package praefect

import (
	"context"
	"crypto/sha1"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func runPraefectWithTransactionMgr(t *testing.T, opts ...transactions.ManagerOpt) (*grpc.ClientConn, *transactions.Manager, testhelper.Cleanup) {
	conf := testConfig(1)

	ds := datastore.Datastore{
		ReplicasDatastore:     datastore.NewInMemory(conf),
		ReplicationEventQueue: datastore.NewMemoryReplicationEventQueue(),
	}

	txMgr := transactions.NewManager(opts...)
	conn, _, cleanup := runPraefectServer(t, conf, ds, txMgr)

	return conn, txMgr, cleanup
}

func setupMetrics() (*prometheus.CounterVec, []transactions.ManagerOpt) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"status"})
	return counter, []transactions.ManagerOpt{
		transactions.WithCounterMetric(counter),
	}
}

type counterMetrics struct {
	registered, started, invalid, committed int
}

func verifyCounterMetrics(t *testing.T, counter *prometheus.CounterVec, expected counterMetrics) {
	t.Helper()

	registered, err := counter.GetMetricWithLabelValues("registered")
	require.NoError(t, err)
	require.Equal(t, float64(expected.registered), testutil.ToFloat64(registered))

	started, err := counter.GetMetricWithLabelValues("started")
	require.NoError(t, err)
	require.Equal(t, float64(expected.started), testutil.ToFloat64(started))

	invalid, err := counter.GetMetricWithLabelValues("invalid")
	require.NoError(t, err)
	require.Equal(t, float64(expected.invalid), testutil.ToFloat64(invalid))

	committed, err := counter.GetMetricWithLabelValues("committed")
	require.NoError(t, err)
	require.Equal(t, float64(expected.committed), testutil.ToFloat64(committed))
}

func TestTransactionSucceeds(t *testing.T) {
	counter, opts := setupMetrics()

	cc, txMgr, cleanup := runPraefectWithTransactionMgr(t, opts...)
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

	verifyCounterMetrics(t, counter, counterMetrics{
		registered: 1,
		started:    1,
		committed:  1,
	})
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
	counter, opts := setupMetrics()

	cc, _, cleanup := runPraefectWithTransactionMgr(t, opts...)
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

	verifyCounterMetrics(t, counter, counterMetrics{
		started: 1,
		invalid: 1,
	})
}

func TestTransactionCancellation(t *testing.T) {
	counter, opts := setupMetrics()

	cc, txMgr, cleanup := runPraefectWithTransactionMgr(t, opts...)
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

	verifyCounterMetrics(t, counter, counterMetrics{
		registered: 1,
		started:    1,
		invalid:    1,
	})
}
