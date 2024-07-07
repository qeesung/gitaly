package internalgitaly

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRunCommand(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	testRepo, testRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           "a",
	})

	testLocalRepo := localrepo.NewTestRepo(t, cfg, testRepo)

	commitID := gittest.WriteCommit(
		t,
		cfg,
		testRepoPath,
		gittest.WithMessage("hello world"),
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{
				Mode:    "100644",
				Path:    ".gitattributes",
				Content: "a/b/c foo=bar",
			},
		),
	)
	commitData, err := testLocalRepo.ReadObject(ctx, commitID)
	require.NoError(t, err)

	srv := NewServer(&service.Dependencies{
		Logger:         testhelper.SharedLogger(t),
		Cfg:            cfg,
		StorageLocator: config.NewLocator(cfg),
		GitCmdFactory:  gittest.NewCommandFactory(t, cfg),
	})

	client := setupInternalGitalyService(t, cfg, srv)

	testCases := []struct {
		desc               string
		req                *gitalypb.RunCommandRequest
		expectedExitStatus int32
		expectedOutput     []byte
	}{
		{
			desc: "git cat-file",
			req: &gitalypb.RunCommandRequest{
				Repository: testRepo,
				GitCommand: &gitalypb.GitCommand{
					Name:  "cat-file",
					Flags: []string{"-p"},
					Args:  []string{git.DefaultBranch},
				},
			},
			expectedExitStatus: 0,
			expectedOutput:     commitData,
		},
		{
			desc: "reading a non-existent object",
			req: &gitalypb.RunCommandRequest{
				Repository: testRepo,
				GitCommand: &gitalypb.GitCommand{
					Name:  "cat-file",
					Flags: []string{"-p"},
					Args:  []string{"does-not-exist"},
				},
			},
			expectedExitStatus: 128,
		},
		{
			desc: "attributes",
			req: &gitalypb.RunCommandRequest{
				Repository: testRepo,
				GitCommand: &gitalypb.GitCommand{
					Name:              "check-attr",
					Flags:             []string{"--source=HEAD"},
					Args:              []string{"foo"},
					PostSeparatorArgs: []string{"a/b/c"},
				},
			},
			expectedExitStatus: 0,
			expectedOutput:     []byte("a/b/c: foo: bar"),
		},
	}

	for _, tc := range testCases {
		resp, err := client.RunCommand(ctx, tc.req)
		require.NoError(t, err)
		require.Equal(t, tc.expectedExitStatus, resp.ReturnCode)
		require.Equal(t, tc.expectedOutput, resp.Output)
	}
}
