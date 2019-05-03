package praefect_test

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitaly/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

// TestReplicatorProcessJobs verifies that a replicator will schedule jobs for
// all whitelisted repos
func TestReplicatorProcessJobsWhitelist(t *testing.T) {
	var (
		cfg = config.Config{
			PrimaryServer: &config.GitalyServer{
				Name:       "default",
				ListenAddr: "tcp://gitaly-primary.example.com",
			},
			SecondaryServers: []*config.GitalyServer{
				{
					Name:       "backup1",
					ListenAddr: "tcp://gitaly-backup1.example.com",
				},
				{
					Name:       "backup2",
					ListenAddr: "tcp://gitaly-backup2.example.com",
				},
			},
			Whitelist: []string{
				"abcd1234",
				"edfg5678",
			},
		}
		datastore   = praefect.NewMemoryDatastore(cfg)
		coordinator = praefect.NewCoordinator(logrus.New(), cfg.PrimaryServer.Name)
		resultsCh   = make(chan result)
		replman     = praefect.NewReplMgr(
			cfg.SecondaryServers[1].Name,
			logrus.New(),
			datastore,
			coordinator,
			praefect.WithWhitelist(cfg.Whitelist),
			praefect.WithReplicator(&mockReplicator{resultsCh}),
		)
	)

	for _, node := range []*config.GitalyServer{
		cfg.PrimaryServer,
		cfg.SecondaryServers[0],
		cfg.SecondaryServers[1],
	} {
		err := coordinator.RegisterNode(node.Name, node.ListenAddr)
		require.NoError(t, err)
	}

	ctx, cancel := testhelper.Context()

	errQ := make(chan error)

	go func() {
		errQ <- replman.ProcessBacklog(ctx)
	}()

	success := make(chan struct{})

	go func() {
		// we expect one job per whitelisted repo with each backend server
		for i := 0; i < len(cfg.Whitelist); i++ {
			result := <-resultsCh

			assert.Contains(t, cfg.Whitelist, result.source.RelativePath)
			assert.Equal(t, result.target.Storage, cfg.SecondaryServers[1].Name)
			assert.Equal(t, result.source.Storage, cfg.PrimaryServer.Name)
		}

		cancel()
		require.EqualError(t, <-errQ, context.Canceled.Error())
		success <- struct{}{}
	}()

	select {

	case <-success:
		return

	case <-time.After(time.Second):
		t.Fatalf("unable to iterate over expected jobs")

	}

}

type result struct {
	source praefect.Repository
	target praefect.Node
}

type mockReplicator struct {
	resultsCh chan<- result
}

func (mr *mockReplicator) Replicate(ctx context.Context, source praefect.Repository, target praefect.Node) error {
	select {

	case mr.resultsCh <- result{source, target}:
		return nil

	case <-ctx.Done():
		return ctx.Err()

	}

	return nil
}
