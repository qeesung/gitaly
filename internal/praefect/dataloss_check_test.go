package praefect

import (
	"testing"

	"github.com/golang/protobuf/ptypes"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/mock"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestDatalossCheck(t *testing.T) {
	const virtualStorage = "praefect"
	cfg := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: virtualStorage,
				Nodes: []*config.Node{
					{
						DefaultPrimary: true,
						Storage:        "not-needed",
						Address:        "tcp::/this-doesnt-matter",
					},
				},
			},
		},
	}

	ctx, cancel := testhelper.Context()
	defer cancel()

	rq := datastore.NewMemoryReplicationEventQueue(cfg)
	const targetNode = "test-node"
	killJobs := func(t *testing.T) {
		t.Helper()
		for {
			jobs, err := rq.Dequeue(ctx, virtualStorage, targetNode, 1)
			require.NoError(t, err)
			if len(jobs) == 0 {
				// all jobs dead
				break
			}

			state := datastore.JobStateFailed
			if jobs[0].Attempt == 0 {
				state = datastore.JobStateDead
			}

			_, err = rq.Acknowledge(ctx, state, []uint64{jobs[0].ID})
			require.NoError(t, err)
		}
	}

	beforeTimerange, err := rq.Enqueue(ctx, datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			RelativePath:   "repo/before-timerange",
			VirtualStorage: virtualStorage,
		},
	})
	require.NoError(t, err)
	expectedDeadJobs := map[string]int64{"repo/dead-job": 1, "repo/multiple-dead-jobs": 2}
	for relPath, count := range expectedDeadJobs {
		for i := int64(0); i < count; i++ {
			_, err := rq.Enqueue(ctx, datastore.ReplicationEvent{
				Job: datastore.ReplicationJob{
					RelativePath:      relPath,
					TargetNodeStorage: targetNode,
					VirtualStorage:    virtualStorage,
				},
			})
			require.NoError(t, err)
		}
	}
	killJobs(t)

	// add some non-dead jobs
	for relPath, state := range map[string]datastore.JobState{
		"repo/completed-job": datastore.JobStateCompleted,
		"repo/cancelled-job": datastore.JobStateCancelled,
	} {
		_, err := rq.Enqueue(ctx, datastore.ReplicationEvent{
			Job: datastore.ReplicationJob{
				RelativePath:      relPath,
				TargetNodeStorage: targetNode,
				VirtualStorage:    virtualStorage,
			},
		})
		require.NoError(t, err)

		jobs, err := rq.Dequeue(ctx, virtualStorage, targetNode, 1)
		require.NoError(t, err)

		_, err = rq.Acknowledge(ctx, state, []uint64{jobs[0].ID})
		require.NoError(t, err)
	}

	afterTimerange, err := rq.Enqueue(ctx, datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			RelativePath: "repo/after-timerange",
		},
	})
	require.NoError(t, err)
	killJobs(t)

	cc, _, clean := runPraefectServerWithMock(t, cfg, rq, map[string]mock.SimpleServiceServer{
		"not-needed": &mock.UnimplementedSimpleServiceServer{},
	})
	defer clean()

	pbFrom, err := ptypes.TimestampProto(beforeTimerange.CreatedAt)
	require.NoError(t, err)
	pbTo, err := ptypes.TimestampProto(afterTimerange.CreatedAt)
	require.NoError(t, err)

	resp, err := gitalypb.NewPraefectInfoServiceClient(cc).DatalossCheck(ctx, &gitalypb.DatalossCheckRequest{
		From: pbFrom,
		To:   pbTo,
	})
	require.NoError(t, err)
	require.Equal(t, &gitalypb.DatalossCheckResponse{ByRelativePath: expectedDeadJobs}, resp)
}
