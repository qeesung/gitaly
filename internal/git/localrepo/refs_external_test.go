package localrepo_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

func setupRepoWithHooksServer(t *testing.T, ctx context.Context, cfg config.Cfg, opts ...testserver.GitalyServerOpt) (string, *localrepo.Repo) {
	testcfg.BuildGitalyHooks(t, cfg)

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterHookServiceServer(srv, hook.NewServer(deps))
	}, opts...)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)
	repo := localrepo.New(testhelper.NewLogger(t), config.NewLocator(cfg), gitCmdFactory, catfileCache, repoProto)

	return repoPath, repo
}

func TestRepo_SetDefaultBranch(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc        string
		ref         git.ReferenceName
		expectedRef git.ReferenceName
	}{
		{
			desc:        "update the branch ref",
			ref:         "refs/heads/feature",
			expectedRef: "refs/heads/feature",
		},
		{
			desc:        "unknown ref",
			ref:         "refs/heads/non_existent_ref",
			expectedRef: "refs/heads/non_existent_ref",
		},
	}
	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t)

			gittest.SkipIfGitVersionLessThan(t, ctx, cfg, git.NewVersion(99, 99, 99, 0), "This requires symref-update in Git")

			txManager := transaction.NewTrackingManager()
			txManager.Reset()
			ctx, err := txinfo.InjectTransaction(
				peer.NewContext(ctx, &peer.Peer{}),
				1,
				"node",
				true,
			)
			require.NoError(t, err)

			repoPath, repo := setupRepoWithHooksServer(t, ctx, cfg, testserver.WithTransactionManager(txManager))

			gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))
			gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"))

			require.NoError(t, repo.SetDefaultBranch(ctx, txManager, tc.ref))

			newRef, err := repo.HeadReference(ctx)
			require.NoError(t, err)

			require.Equal(t, tc.expectedRef, newRef)

			require.Len(t, txManager.Votes(), 2)
			h := voting.NewVoteHash()
			_, err = h.Write([]byte(fmt.Sprintf("%s ref:%s %s\n", gittest.DefaultObjectHash.ZeroOID, tc.ref.String(), "HEAD")))
			require.NoError(t, err)
			vote, err := h.Vote()
			require.NoError(t, err)

			require.Equal(t, voting.Prepared, txManager.Votes()[0].Phase)
			require.Equal(t, vote.String(), txManager.Votes()[0].Vote.String())
			require.Equal(t, voting.Committed, txManager.Votes()[1].Phase)
			require.Equal(t, vote.String(), txManager.Votes()[1].Vote.String())
		})
	}
}

func TestRepo_SetDefaultBranch_errors(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	t.Run("malformed refname", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		gittest.SkipIfGitVersionLessThan(t, ctx, cfg, git.NewVersion(99, 99, 99, 0), "This requires symref-update in Git")

		_, repo := setupRepoWithHooksServer(t, ctx, cfg)

		invalidRefname := "./.lock"

		err := repo.SetDefaultBranch(ctx, &transaction.MockManager{}, git.ReferenceName(invalidRefname))
		require.ErrorIs(t, err, updateref.InvalidReferenceFormatError{ReferenceName: invalidRefname})
	})

	t.Run("HEAD is locked by another process", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		gittest.SkipIfGitVersionLessThan(t, ctx, cfg, git.NewVersion(99, 99, 99, 0), "This requires symref-update in Git")

		_, repo := setupRepoWithHooksServer(t, ctx, cfg)

		ref, err := repo.HeadReference(ctx)
		require.NoError(t, err)

		version, err := repo.GitVersion(ctx)
		require.NoError(t, err)

		updater, err := updateref.New(ctx, repo)
		require.NoError(t, err)

		require.NoError(t, updater.Start())
		require.NoError(t, updater.SymrefUpdate(version, "HEAD", "refs/heads/temp"))
		require.NoError(t, updater.Prepare())
		t.Cleanup(func() { require.NoError(t, updater.Close()) })

		err = repo.SetDefaultBranch(ctx, &transaction.MockManager{}, "refs/heads/branch")
		require.ErrorIs(t, err, gittest.FilesOrReftables(
			updateref.AlreadyLockedError{ReferenceName: "HEAD"},
			updateref.AlreadyLockedError{},
		))

		refAfter, err := repo.HeadReference(ctx)
		require.NoError(t, err)
		require.Equal(t, ref, refAfter)
	})

	t.Run("HEAD is locked by SetDefaultBranch", func(t *testing.T) {
		t.Parallel()

		testhelper.SkipWithReftable(t, "reftable doesn't add HEAD.lock")
		cfg := testcfg.Build(t)
		gittest.SkipIfGitVersionLessThan(t, ctx, cfg, git.NewVersion(99, 99, 99, 0), "This requires symref-update in Git")

		ctx, err := txinfo.InjectTransaction(
			peer.NewContext(ctx, &peer.Peer{}),
			1,
			"node",
			true,
		)
		require.NoError(t, err)

		ch := make(chan struct{})
		doneCh := make(chan struct{})

		_, repo := setupRepoWithHooksServer(t, ctx, cfg, testserver.WithTransactionManager(&blockingManager{ch}))

		go func() {
			_ = repo.SetDefaultBranch(ctx, nil, "refs/heads/branch")
			doneCh <- struct{}{}
		}()
		<-ch

		var stderr bytes.Buffer
		err = repo.ExecAndWait(ctx, git.Command{
			Name: "symbolic-ref",
			Args: []string{"HEAD", "refs/heads/otherbranch"},
		}, git.WithRefTxHook(repo), git.WithStderr(&stderr))

		code, ok := command.ExitStatus(err)
		require.True(t, ok)
		assert.Equal(t, 1, code)
		assert.Regexp(t, "Unable to create .+\\/HEAD\\.lock': File exists.", stderr.String())
		ch <- struct{}{}
		<-doneCh
	})

	t.Run("failing vote unlocks symref", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		gittest.SkipIfGitVersionLessThan(t, ctx, cfg, git.NewVersion(99, 99, 99, 0), "This requires symref-update in Git")

		ctx, err := txinfo.InjectTransaction(
			peer.NewContext(ctx, &peer.Peer{}),
			1,
			"node",
			true,
		)
		require.NoError(t, err)

		failingTxManager := &transaction.MockManager{
			VoteFn: func(context.Context, txinfo.Transaction, voting.Vote, voting.Phase) error {
				return errors.New("injected error")
			},
		}

		_, repo := setupRepoWithHooksServer(t, ctx, cfg, testserver.WithTransactionManager(failingTxManager))

		err = repo.SetDefaultBranch(ctx, nil, "refs/heads/branch")
		require.Error(t, err)

		var sErr structerr.Error
		require.ErrorAs(t, err, &sErr)
		require.Contains(t, sErr.Metadata(), "stderr")
		require.Equal(t, "error executing git hook\nfatal: ref updates aborted by hook\n", sErr.Metadata()["stderr"])
	})
}

type blockingManager struct {
	ch chan struct{}
}

func (b *blockingManager) Vote(_ context.Context, _ txinfo.Transaction, _ voting.Vote, phase voting.Phase) error {
	// the purpose of this is to block SetDefaultBranch from completing, so just choose to block on
	// a Prepared vote.
	if phase == voting.Prepared {
		b.ch <- struct{}{}
		<-b.ch
	}

	return nil
}

func (b *blockingManager) Stop(_ context.Context, _ txinfo.Transaction) error {
	return nil
}
