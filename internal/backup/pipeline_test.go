package backup

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestPipeline(t *testing.T) {
	t.Parallel()

	// Sequential
	testPipeline(t, func() *Pipeline {
		p, err := NewPipeline(testhelper.SharedLogger(t))
		require.NoError(t, err)
		return p
	})

	// Concurrent
	t.Run("parallelism", func(t *testing.T) {
		for _, tc := range []struct {
			parallel                   int
			parallelStorage            int
			expectedMaxParallel        int
			expectedMaxStorageParallel int
		}{
			{
				parallel:                   2,
				parallelStorage:            0,
				expectedMaxParallel:        2,
				expectedMaxStorageParallel: 2,
			},
			{
				parallel:                   2,
				parallelStorage:            3,
				expectedMaxParallel:        2,
				expectedMaxStorageParallel: 2,
			},
			{
				parallel:                   0,
				parallelStorage:            3,
				expectedMaxParallel:        6, // 2 storages * 3 workers per storage
				expectedMaxStorageParallel: 3,
			},
			{
				parallel:                   3,
				parallelStorage:            2,
				expectedMaxParallel:        3,
				expectedMaxStorageParallel: 2,
			},
		} {
			t.Run(fmt.Sprintf("parallel:%d,parallelStorage:%d", tc.parallel, tc.parallelStorage), func(t *testing.T) {
				var mu sync.Mutex
				// callsPerStorage tracks the number of concurrent jobs running for each storage.
				callsPerStorage := map[string]int{
					"storage1": 0,
					"storage2": 0,
				}

				strategy := MockStrategy{
					CreateFunc: func(ctx context.Context, req *CreateRequest) error {
						mu.Lock()
						callsPerStorage[req.Repository.StorageName]++
						allCalls := 0
						for _, v := range callsPerStorage {
							allCalls += v
						}
						// We ensure that the concurrency for each storage is not above the
						// parallelStorage threshold, and also that the total number of concurrent
						// jobs is not above the parallel threshold.
						require.LessOrEqual(t, callsPerStorage[req.Repository.StorageName], tc.expectedMaxStorageParallel)
						require.LessOrEqual(t, allCalls, tc.expectedMaxParallel)
						mu.Unlock()
						defer func() {
							mu.Lock()
							callsPerStorage[req.Repository.StorageName]--
							mu.Unlock()
						}()

						time.Sleep(time.Millisecond)
						return nil
					},
				}
				p, err := NewPipeline(testhelper.SharedLogger(t), WithConcurrency(tc.parallel, tc.parallelStorage))
				require.NoError(t, err)
				ctx := testhelper.Context(t)

				for i := 0; i < 10; i++ {
					p.Handle(ctx, NewCreateCommand(strategy, CreateRequest{Repository: &gitalypb.Repository{StorageName: "storage1"}}))
					p.Handle(ctx, NewCreateCommand(strategy, CreateRequest{Repository: &gitalypb.Repository{StorageName: "storage2"}}))
				}
				_, err = p.Done()
				require.NoError(t, err)
			})
		}
	})

	t.Run("context done", func(t *testing.T) {
		var strategy MockStrategy
		p, err := NewPipeline(testhelper.SharedLogger(t))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(testhelper.Context(t))

		cancel()
		<-ctx.Done()

		p.Handle(ctx, NewCreateCommand(strategy, CreateRequest{Repository: &gitalypb.Repository{StorageName: "default"}}))

		_, err = p.Done()
		require.EqualError(t, err, "pipeline: context canceled")
	})
}

type MockStrategy struct {
	CreateFunc  func(context.Context, *CreateRequest) error
	RestoreFunc func(context.Context, *RestoreRequest) error
}

func (s MockStrategy) Create(ctx context.Context, req *CreateRequest) error {
	if s.CreateFunc != nil {
		return s.CreateFunc(ctx, req)
	}
	return nil
}

func (s MockStrategy) Restore(ctx context.Context, req *RestoreRequest) error {
	if s.RestoreFunc != nil {
		return s.RestoreFunc(ctx, req)
	}
	return nil
}

func testPipeline(t *testing.T, init func() *Pipeline) {
	strategy := MockStrategy{
		CreateFunc: func(_ context.Context, req *CreateRequest) error {
			switch req.Repository.StorageName {
			case "normal":
				return nil
			case "skip":
				return ErrSkipped
			case "error":
				return assert.AnError
			}
			require.Failf(t, "unexpected call to Create", "StorageName = %q", req.Repository.StorageName)
			return nil
		},
	}

	for _, tc := range []struct {
		desc           string
		command        Command
		level          logrus.Level
		expectedFields log.Fields
	}{
		{
			desc:    "Create command. Normal repository",
			command: NewCreateCommand(strategy, CreateRequest{Repository: &gitalypb.Repository{RelativePath: "a.git", StorageName: "normal"}}),
			level:   logrus.InfoLevel,
			expectedFields: log.Fields{
				"command":         "create",
				"gl_project_path": "",
				"relative_path":   "a.git",
				"storage_name":    "normal",
			},
		},
		{
			desc:    "Create command. Skipped repository",
			command: NewCreateCommand(strategy, CreateRequest{Repository: &gitalypb.Repository{RelativePath: "b.git", StorageName: "skip"}}),
			level:   logrus.WarnLevel,
			expectedFields: log.Fields{
				"command":         "create",
				"gl_project_path": "",
				"relative_path":   "b.git",
				"storage_name":    "skip",
			},
		},
		{
			desc:    "Create command. Error creating repository",
			command: NewCreateCommand(strategy, CreateRequest{Repository: &gitalypb.Repository{RelativePath: "c.git", StorageName: "error"}}),
			level:   logrus.ErrorLevel,
			expectedFields: log.Fields{
				"command":         "create",
				"gl_project_path": "",
				"relative_path":   "c.git",
				"storage_name":    "error",
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			logger := testhelper.SharedLogger(t)
			loggerHook := testhelper.AddLoggerHook(logger)

			t.Parallel()

			p := init()
			ctx := testhelper.Context(t)

			p.Handle(ctx, tc.command)

			logEntries := loggerHook.AllEntries()

			for _, logEntry := range logEntries {
				require.Equal(t, tc.expectedFields, logEntry.Data)
				require.Equal(t, tc.level, logEntry.Level)
			}

			_, err := p.Done()

			if tc.level == logrus.ErrorLevel {
				require.EqualError(t, err, "pipeline: 1 failures encountered:\n - c.git: assert.AnError general error for testing\n")
			}
		})
	}

	t.Run("restore command", func(t *testing.T) {
		t.Parallel()

		strategy := MockStrategy{
			RestoreFunc: func(_ context.Context, req *RestoreRequest) error {
				switch req.Repository.StorageName {
				case "normal":
					return nil
				case "skip":
					return ErrSkipped
				case "error":
					return assert.AnError
				}
				require.Failf(t, "unexpected call to Restore", "StorageName = %q", req.Repository.StorageName)
				return nil
			},
		}
		p := init()
		ctx := testhelper.Context(t)

		commands := []Command{
			NewRestoreCommand(strategy, RestoreRequest{Repository: &gitalypb.Repository{RelativePath: "a.git", StorageName: "normal"}}),
			NewRestoreCommand(strategy, RestoreRequest{Repository: &gitalypb.Repository{RelativePath: "b.git", StorageName: "skip"}}),
			NewRestoreCommand(strategy, RestoreRequest{Repository: &gitalypb.Repository{RelativePath: "c.git", StorageName: "error"}}),
		}
		for _, cmd := range commands {
			p.Handle(ctx, cmd)
		}
		_, err := p.Done()
		require.EqualError(t, err, "pipeline: 1 failures encountered:\n - c.git: assert.AnError general error for testing\n")
	})
}

func TestPipelineError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		repos         []*gitalypb.Repository
		expectedError string
	}{
		{
			name: "with gl_project_path",
			repos: []*gitalypb.Repository{
				{RelativePath: "1.git", GlProjectPath: "Projects/Apple"},
				{RelativePath: "2.git", GlProjectPath: "Projects/Banana"},
				{RelativePath: "3.git", GlProjectPath: "Projects/Carrot"},
			},
			expectedError: `3 failures encountered:
 - 1.git (Projects/Apple): assert.AnError general error for testing
 - 2.git (Projects/Banana): assert.AnError general error for testing
 - 3.git (Projects/Carrot): assert.AnError general error for testing
`,
		},
		{
			name: "without gl_project_path",
			repos: []*gitalypb.Repository{
				{RelativePath: "1.git"},
				{RelativePath: "2.git"},
				{RelativePath: "3.git"},
			},
			expectedError: `3 failures encountered:
 - 1.git: assert.AnError general error for testing
 - 2.git: assert.AnError general error for testing
 - 3.git: assert.AnError general error for testing
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := &commandErrors{}

			for _, repo := range tc.repos {
				err.AddError(repo, assert.AnError)
			}

			require.EqualError(t, err, tc.expectedError)
		})
	}
}

func TestPipelineProcessedRepos(t *testing.T) {
	strategy := MockStrategy{}

	repos := []*gitalypb.Repository{
		{RelativePath: "a.git", StorageName: "storage1"},
		{RelativePath: "b.git", StorageName: "storage1"},
		{RelativePath: "c.git", StorageName: "storage2"},
		{RelativePath: "d.git", StorageName: "storage3"},
	}

	expectedProcessedRepos := map[string]map[repositoryKey]struct{}{
		"storage1": {
			"storage1-a.git": {},
			"storage1-b.git": {},
		},
		"storage2": {
			"storage2-c.git": {},
		},
		"storage3": {
			"storage3-d.git": {},
		},
	}

	p, err := NewPipeline(testhelper.SharedLogger(t))
	require.NoError(t, err)

	ctx := testhelper.Context(t)
	for _, repo := range repos {
		p.Handle(ctx, NewRestoreCommand(strategy, RestoreRequest{Repository: repo}))
	}

	processedRepos, err := p.Done()
	require.NoError(t, err)
	require.EqualValues(t, expectedProcessedRepos, processedRepos)
}
