package bundleuri

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestSink_Generate(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc        string
		setup       func(t *testing.T, repoPath string)
		expectedErr error
	}{
		{
			desc: "creates bundle successfully",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "README", Content: "much"}),
					gittest.WithBranch("main"))
			},
		},
		{
			desc:        "fails with missing HEAD",
			setup:       func(t *testing.T, repoPath string) {},
			expectedErr: structerr.NewFailedPrecondition("ref %q does not exist: %w", "refs/heads/main", fmt.Errorf("create bundle: %w", localrepo.ErrEmptyBundle)),
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			tc.setup(t, repoPath)

			sinkDir := t.TempDir()
			sink, err := NewSink(ctx, "file://"+sinkDir)
			require.NoError(t, err)

			err = sink.Generate(ctx, repo)
			if tc.expectedErr == nil {
				require.NoError(t, err)
				require.FileExists(t, filepath.Join(sinkDir, sink.relativePath(repo, "default")))
			} else {
				require.Equal(t, err, tc.expectedErr, err)
			}
		})
	}
}
