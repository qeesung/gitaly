package praefect

import (
	"context"
	"crypto/sha1"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type voter struct {
	votes         uint
	vote          string
	showsUp       bool
	shouldSucceed bool
}

func runPraefectServerAndTxMgr(t testing.TB, opts ...transactions.ManagerOpt) (*grpc.ClientConn, *transactions.Manager, testhelper.Cleanup) {
	conf := testConfig(1)
	txMgr := transactions.NewManager(opts...)
	cc, _, cleanup := runPraefectServer(t, conf, buildOptions{
		withTxMgr:   txMgr,
		withNodeMgr: nullNodeMgr{}, // to suppress node address issues
	})
	return cc, txMgr, cleanup
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
	cc, txMgr, cleanup := runPraefectServerAndTxMgr(t, opts...)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := gitalypb.NewRefTransactionClient(cc)

	transactionID, cancelTransaction, err := txMgr.RegisterTransaction(ctx, []transactions.Voter{
		{Name: "node1", Votes: 1},
	}, 1)
	require.NoError(t, err)
	require.NotZero(t, transactionID)
	defer cancelTransaction()

	hash := sha1.Sum([]byte{})

	response, err := client.VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{
		TransactionId:        transactionID,
		Node:                 "node1",
		ReferenceUpdatesHash: hash[:],
	})
	require.NoError(t, err)
	require.Equal(t, gitalypb.VoteTransactionResponse_COMMIT, response.State)

	verifyCounterMetrics(t, counter, counterMetrics{
		registered: 1,
		started:    1,
		committed:  1,
	})
}

func TestTransactionWithMultipleNodes(t *testing.T) {
	testcases := []struct {
		desc          string
		nodes         []string
		hashes        [][20]byte
		expectedState gitalypb.VoteTransactionResponse_TransactionState
	}{
		{
			desc: "Nodes with same hash",
			nodes: []string{
				"node1",
				"node2",
			},
			hashes: [][20]byte{
				sha1.Sum([]byte{}),
				sha1.Sum([]byte{}),
			},
			expectedState: gitalypb.VoteTransactionResponse_COMMIT,
		},
		{
			desc: "Nodes with different hashes",
			nodes: []string{
				"node1",
				"node2",
			},
			hashes: [][20]byte{
				sha1.Sum([]byte("foo")),
				sha1.Sum([]byte("bar")),
			},
			expectedState: gitalypb.VoteTransactionResponse_ABORT,
		},
		{
			desc: "More nodes with same hash",
			nodes: []string{
				"node1",
				"node2",
				"node3",
				"node4",
			},
			hashes: [][20]byte{
				sha1.Sum([]byte("foo")),
				sha1.Sum([]byte("foo")),
				sha1.Sum([]byte("foo")),
				sha1.Sum([]byte("foo")),
			},
			expectedState: gitalypb.VoteTransactionResponse_COMMIT,
		},
		{
			desc: "Majority with same hash",
			nodes: []string{
				"node1",
				"node2",
				"node3",
				"node4",
			},
			hashes: [][20]byte{
				sha1.Sum([]byte("foo")),
				sha1.Sum([]byte("foo")),
				sha1.Sum([]byte("bar")),
				sha1.Sum([]byte("foo")),
			},
			expectedState: gitalypb.VoteTransactionResponse_ABORT,
		},
	}

	cc, txMgr, cleanup := runPraefectServerAndTxMgr(t)
	defer cleanup()

	ctx, cleanup := testhelper.Context()
	defer cleanup()

	client := gitalypb.NewRefTransactionClient(cc)

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			var voters []transactions.Voter
			var threshold uint
			for _, node := range tc.nodes {
				voters = append(voters, transactions.Voter{Name: node, Votes: 1})
				threshold += 1
			}

			transactionID, cancelTransaction, err := txMgr.RegisterTransaction(ctx, voters, threshold)
			require.NoError(t, err)
			defer cancelTransaction()

			var wg sync.WaitGroup
			for i := 0; i < len(voters); i++ {
				wg.Add(1)

				go func(idx int) {
					defer wg.Done()

					response, err := client.VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{
						TransactionId:        transactionID,
						Node:                 voters[idx].Name,
						ReferenceUpdatesHash: tc.hashes[idx][:],
					})
					require.NoError(t, err)
					require.Equal(t, tc.expectedState, response.State)
				}(i)
			}

			wg.Wait()
		})
	}
}

func TestTransactionWithContextCancellation(t *testing.T) {
	cc, txMgr, cleanup := runPraefectServerAndTxMgr(t)
	defer cleanup()

	client := gitalypb.NewRefTransactionClient(cc)

	ctx, cancel := testhelper.Context()

	transactionID, cancelTransaction, err := txMgr.RegisterTransaction(ctx, []transactions.Voter{
		{Name: "voter", Votes: 1},
		{Name: "absent", Votes: 1},
	}, 2)
	require.NoError(t, err)
	defer cancelTransaction()

	hash := sha1.Sum([]byte{})

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := client.VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{
			TransactionId:        transactionID,
			Node:                 "voter",
			ReferenceUpdatesHash: hash[:],
		})
		require.Error(t, err)
		require.Equal(t, codes.Canceled, status.Code(err))
	}()

	cancel()
	wg.Wait()
}

func TestTransactionRegistrationWithInvalidNodesFails(t *testing.T) {
	ctx, cleanup := testhelper.Context()
	defer cleanup()

	txMgr := transactions.NewManager()

	_, _, err := txMgr.RegisterTransaction(ctx, []transactions.Voter{}, 1)
	require.Equal(t, transactions.ErrMissingNodes, err)

	_, _, err = txMgr.RegisterTransaction(ctx, []transactions.Voter{
		{Name: "node1", Votes: 1},
		{Name: "node2", Votes: 1},
		{Name: "node1", Votes: 1},
	}, 3)
	require.Equal(t, transactions.ErrDuplicateNodes, err)
}

func TestTransactionRegistrationWithInvalidThresholdFails(t *testing.T) {
	tc := []struct {
		desc      string
		votes     []uint
		threshold uint
	}{
		{
			desc:      "threshold is unreachable",
			votes:     []uint{1, 1},
			threshold: 3,
		},
		{
			desc:      "threshold of zero fails",
			votes:     []uint{0},
			threshold: 0,
		},
		{
			desc:      "threshold smaller than majority fails",
			votes:     []uint{1, 1, 1},
			threshold: 1,
		},
		{
			desc:      "threshold equaling majority fails",
			votes:     []uint{1, 1, 1, 1},
			threshold: 2,
		},
		{
			desc:      "threshold accounts for higher node votes",
			votes:     []uint{2, 2, 2, 2},
			threshold: 4,
		},
	}

	ctx, cleanup := testhelper.Context()
	defer cleanup()

	txMgr := transactions.NewManager()

	for _, tc := range tc {
		t.Run(tc.desc, func(t *testing.T) {
			var voters []transactions.Voter

			for i, votes := range tc.votes {
				voters = append(voters, transactions.Voter{
					Name:  fmt.Sprintf("node-%d", i),
					Votes: votes,
				})
			}

			_, _, err := txMgr.RegisterTransaction(ctx, voters, tc.threshold)
			require.Equal(t, transactions.ErrInvalidThreshold, err)
		})
	}
}

func TestTransactionReachesQuorum(t *testing.T) {
	tc := []struct {
		desc      string
		voters    []voter
		threshold uint
	}{
		{
			desc: "quorum is is not reached without majority",
			voters: []voter{
				{votes: 1, vote: "foo", showsUp: true, shouldSucceed: false},
				{votes: 1, vote: "bar", showsUp: true, shouldSucceed: false},
				{votes: 1, vote: "baz", showsUp: true, shouldSucceed: false},
			},
			threshold: 2,
		},
		{
			desc: "quorum is reached with unweighted node failing",
			voters: []voter{
				{votes: 1, vote: "foo", showsUp: true, shouldSucceed: true},
				{votes: 0, vote: "bar", showsUp: true, shouldSucceed: false},
			},
			threshold: 1,
		},
		{
			desc: "quorum is reached with majority",
			voters: []voter{
				{votes: 1, vote: "foo", showsUp: true, shouldSucceed: true},
				{votes: 1, vote: "foo", showsUp: true, shouldSucceed: true},
				{votes: 1, vote: "bar", showsUp: true, shouldSucceed: false},
			},
			threshold: 2,
		},
		{
			desc: "quorum is reached with high vote outweighing",
			voters: []voter{
				{votes: 3, vote: "foo", showsUp: true, shouldSucceed: true},
				{votes: 1, vote: "bar", showsUp: true, shouldSucceed: false},
				{votes: 1, vote: "bar", showsUp: true, shouldSucceed: false},
			},
			threshold: 3,
		},
		{
			desc: "quorum is reached with high vote being outweighed",
			voters: []voter{
				{votes: 3, vote: "foo", showsUp: true, shouldSucceed: false},
				{votes: 1, vote: "bar", showsUp: true, shouldSucceed: true},
				{votes: 1, vote: "bar", showsUp: true, shouldSucceed: true},
				{votes: 1, vote: "bar", showsUp: true, shouldSucceed: true},
				{votes: 1, vote: "bar", showsUp: true, shouldSucceed: true},
			},
			threshold: 4,
		},
		{
			desc: "quorum is reached with disappearing unweighted voter",
			voters: []voter{
				{votes: 1, vote: "foo", showsUp: true, shouldSucceed: true},
				{votes: 0, vote: "foo", showsUp: false, shouldSucceed: false},
			},
			threshold: 1,
		},
		{
			desc: "quorum is reached with disappearing weighted voter",
			voters: []voter{
				{votes: 1, vote: "foo", showsUp: true, shouldSucceed: true},
				{votes: 1, vote: "foo", showsUp: true, shouldSucceed: true},
				{votes: 1, vote: "bar", showsUp: false, shouldSucceed: false},
			},
			threshold: 2,
		},
	}

	cc, txMgr, cleanup := runPraefectServerAndTxMgr(t)
	defer cleanup()

	ctx, cleanup := testhelper.Context()
	defer cleanup()

	client := gitalypb.NewRefTransactionClient(cc)

	for _, tc := range tc {
		t.Run(tc.desc, func(t *testing.T) {
			var voters []transactions.Voter

			for i, voter := range tc.voters {
				voters = append(voters, transactions.Voter{
					Name:  fmt.Sprintf("node-%d", i),
					Votes: voter.votes,
				})
			}

			transactionID, cancel, err := txMgr.RegisterTransaction(ctx, voters, tc.threshold)
			require.NoError(t, err)
			defer cancel()

			var wg sync.WaitGroup
			for i, v := range tc.voters {
				if !v.showsUp {
					continue
				}

				wg.Add(1)
				go func(i int, v voter) {
					defer wg.Done()

					name := fmt.Sprintf("node-%d", i)
					hash := sha1.Sum([]byte(v.vote))

					response, err := client.VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{
						TransactionId:        transactionID,
						Node:                 name,
						ReferenceUpdatesHash: hash[:],
					})
					require.NoError(t, err)

					if v.shouldSucceed {
						require.Equal(t, gitalypb.VoteTransactionResponse_COMMIT, response.State, "node should have received COMMIT")
					} else {
						require.Equal(t, gitalypb.VoteTransactionResponse_ABORT, response.State, "node should have received ABORT")
					}
				}(i, v)
			}

			wg.Wait()
		})
	}
}

func TestTransactionFailures(t *testing.T) {
	counter, opts := setupMetrics()
	cc, _, cleanup := runPraefectServerAndTxMgr(t, opts...)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := gitalypb.NewRefTransactionClient(cc)

	hash := sha1.Sum([]byte{})
	_, err := client.VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{
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
	testcases := []struct {
		desc            string
		voters          []voter
		threshold       uint
		expectedMetrics counterMetrics
	}{
		{
			desc: "single node cancellation",
			voters: []voter{
				{votes: 1, showsUp: false, shouldSucceed: false},
			},
			threshold:       1,
			expectedMetrics: counterMetrics{registered: 1, committed: 0},
		},
		{
			desc: "two nodes failing to show up",
			voters: []voter{
				{votes: 1, showsUp: false, shouldSucceed: false},
				{votes: 1, showsUp: false, shouldSucceed: false},
			},
			threshold:       2,
			expectedMetrics: counterMetrics{registered: 2, committed: 0},
		},
		{
			desc: "two nodes with unweighted node failing",
			voters: []voter{
				{votes: 1, showsUp: true, shouldSucceed: true},
				{votes: 0, showsUp: false, shouldSucceed: false},
			},
			threshold:       1,
			expectedMetrics: counterMetrics{registered: 2, started: 1, committed: 1},
		},
		{
			desc: "multiple weighted votes with subset failing",
			voters: []voter{
				{votes: 1, showsUp: true, shouldSucceed: true},
				{votes: 1, showsUp: true, shouldSucceed: true},
				{votes: 1, showsUp: false, shouldSucceed: false},
			},
			threshold:       2,
			expectedMetrics: counterMetrics{registered: 3, started: 2, committed: 2},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			counter, opts := setupMetrics()
			cc, txMgr, cleanup := runPraefectServerAndTxMgr(t, opts...)
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			client := gitalypb.NewRefTransactionClient(cc)

			voters := make([]transactions.Voter, 0, len(tc.voters))
			for i, voter := range tc.voters {
				voters = append(voters, transactions.Voter{
					Name:  fmt.Sprintf("node-%d", i),
					Votes: voter.votes,
				})
			}

			transactionID, cancelTransaction, err := txMgr.RegisterTransaction(ctx, voters, tc.threshold)
			require.NoError(t, err)

			var wg sync.WaitGroup
			for i, v := range tc.voters {
				if !v.showsUp {
					continue
				}

				wg.Add(1)
				go func(i int, v voter) {
					defer wg.Done()

					name := fmt.Sprintf("node-%d", i)
					hash := sha1.Sum([]byte(v.vote))

					response, err := client.VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{
						TransactionId:        transactionID,
						Node:                 name,
						ReferenceUpdatesHash: hash[:],
					})
					require.NoError(t, err)

					if v.shouldSucceed {
						require.Equal(t, gitalypb.VoteTransactionResponse_COMMIT, response.State, "node should have received COMMIT")
					} else {
						require.Equal(t, gitalypb.VoteTransactionResponse_ABORT, response.State, "node should have received ABORT")
					}
				}(i, v)
			}
			wg.Wait()

			results, err := cancelTransaction()
			require.NoError(t, err)

			for i, v := range tc.voters {
				require.Equal(t, results[fmt.Sprintf("node-%d", i)], v.shouldSucceed, "result mismatches expected node state")
			}

			verifyCounterMetrics(t, counter, tc.expectedMetrics)
		})
	}
}
