package hook

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestUpdate_customHooks(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	commitA := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("a"))
	commitB := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("b"))

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	locator := config.NewLocator(cfg)

	txManager := transaction.NewTrackingManager()
	hookManager := NewManager(cfg, locator, testhelper.SharedLogger(t), gitCmdFactory, txManager, gitlab.NewMockClient(
		t, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive,
	), NewTransactionRegistry(storagemgr.NewTransactionRegistry()))

	receiveHooksPayload := &git.UserDetails{
		UserID:   "1234",
		Username: "user",
		Protocol: "web",
	}

	payload, err := git.NewHooksPayload(
		cfg,
		repo,
		gittest.DefaultObjectHash,
		nil,
		receiveHooksPayload,
		git.UpdateHook,
		featureflag.FromContext(ctx),
		storage.ExtractTransactionID(ctx),
	).Env()
	require.NoError(t, err)

	primaryPayload, err := git.NewHooksPayload(
		cfg,
		repo,
		gittest.DefaultObjectHash,
		&txinfo.Transaction{
			ID: 1234, Node: "primary", Primary: true,
		},
		receiveHooksPayload,
		git.UpdateHook,
		featureflag.FromContext(ctx),
		storage.ExtractTransactionID(ctx),
	).Env()
	require.NoError(t, err)

	secondaryPayload, err := git.NewHooksPayload(
		cfg,
		repo,
		gittest.DefaultObjectHash,
		&txinfo.Transaction{
			ID: 1234, Node: "secondary", Primary: false,
		},
		receiveHooksPayload,
		git.UpdateHook,
		featureflag.FromContext(ctx),
		storage.ExtractTransactionID(ctx),
	).Env()
	require.NoError(t, err)

	testCases := []struct {
		desc           string
		env            []string
		hook           string
		reference      string
		oldHash        git.ObjectID
		newHash        git.ObjectID
		expectedErr    string
		expectedStdout string
		expectedStderr string
		expectedVotes  []transaction.PhasedVote
	}{
		{
			desc:           "hook receives environment variables",
			env:            []string{payload},
			reference:      "refs/heads/master",
			oldHash:        commitA,
			newHash:        commitB,
			hook:           "#!/bin/sh\nenv | grep -v -e '^SHLVL=' -e '^_=' | sort\n",
			expectedStdout: strings.Join(getExpectedEnv(t, ctx, locator, gitCmdFactory, repo), "\n") + "\n",
			expectedVotes:  []transaction.PhasedVote{},
		},
		{
			desc:           "hook receives arguments",
			env:            []string{payload},
			reference:      "refs/heads/master",
			oldHash:        commitA,
			newHash:        commitB,
			hook:           "#!/bin/sh\nprintf '%s\\n' \"$@\"\n",
			expectedStdout: fmt.Sprintf("refs/heads/master\n%s\n%s\n", commitA, commitB),
			expectedVotes:  []transaction.PhasedVote{},
		},
		{
			desc:           "stdout and stderr are passed through",
			env:            []string{payload},
			reference:      "refs/heads/master",
			oldHash:        commitA,
			newHash:        commitB,
			hook:           "#!/bin/sh\necho foo >&1\necho bar >&2\n",
			expectedStdout: "foo\n",
			expectedStderr: "bar\n",
			expectedVotes:  []transaction.PhasedVote{},
		},
		{
			desc:          "standard input is empty",
			env:           []string{payload},
			reference:     "refs/heads/master",
			oldHash:       commitA,
			newHash:       commitB,
			hook:          "#!/bin/sh\ncat\n",
			expectedVotes: []transaction.PhasedVote{},
		},
		{
			desc:          "invalid script causes failure",
			env:           []string{payload},
			reference:     "refs/heads/master",
			oldHash:       commitA,
			newHash:       commitB,
			hook:          "",
			expectedErr:   "exec format error",
			expectedVotes: []transaction.PhasedVote{},
		},
		{
			desc:          "errors are passed through",
			env:           []string{payload},
			reference:     "refs/heads/master",
			oldHash:       commitA,
			newHash:       commitB,
			hook:          "#!/bin/sh\nexit 123\n",
			expectedErr:   "exit status 123",
			expectedVotes: []transaction.PhasedVote{},
		},
		{
			desc:           "errors are passed through with stderr and stdout",
			env:            []string{payload},
			reference:      "refs/heads/master",
			oldHash:        commitA,
			newHash:        commitB,
			hook:           "#!/bin/sh\necho foo >&1\necho bar >&2\nexit 123\n",
			expectedStdout: "foo\n",
			expectedStderr: "bar\n",
			expectedErr:    "exit status 123",
			expectedVotes:  []transaction.PhasedVote{},
		},
		{
			desc:           "hook is executed on primary",
			env:            []string{primaryPayload},
			reference:      "refs/heads/master",
			oldHash:        commitA,
			newHash:        commitB,
			hook:           "#!/bin/sh\necho foo\n",
			expectedStdout: "foo\n",
			expectedVotes:  []transaction.PhasedVote{synchronizedVote("update")},
		},
		{
			desc:          "hook is not executed on secondary",
			env:           []string{secondaryPayload},
			reference:     "refs/heads/master",
			oldHash:       commitA,
			newHash:       commitB,
			hook:          "#!/bin/sh\necho foo\n",
			expectedVotes: []transaction.PhasedVote{synchronizedVote("update")},
		},
		{
			desc:          "hook fails with missing reference",
			env:           []string{payload},
			oldHash:       commitA,
			newHash:       commitB,
			expectedErr:   "hook got no reference",
			expectedVotes: []transaction.PhasedVote{},
		},
		{
			desc:          "hook fails with missing old value",
			env:           []string{payload},
			reference:     "refs/heads/master",
			newHash:       commitB,
			expectedErr:   "hook got invalid old value",
			expectedVotes: []transaction.PhasedVote{},
		},
		{
			desc:          "hook fails with missing new value",
			env:           []string{payload},
			reference:     "refs/heads/master",
			oldHash:       commitA,
			expectedErr:   "hook got invalid new value",
			expectedVotes: []transaction.PhasedVote{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			txManager.Reset()

			gittest.WriteCustomHook(t, repoPath, "update", []byte(tc.hook))

			var stdout, stderr bytes.Buffer
			err := hookManager.UpdateHook(ctx, repo, tc.reference, tc.oldHash.String(), tc.newHash.String(), tc.env, &stdout, &stderr)
			if tc.expectedErr != "" {
				require.Contains(t, err.Error(), tc.expectedErr)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.expectedStdout, stdout.String())
			require.Equal(t, tc.expectedStderr, stderr.String())
			require.Equal(t, tc.expectedVotes, txManager.Votes())
		})
	}
}

func TestUpdate_quarantine(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	quarantine, _, err := quarantine.New(ctx, repoProto, testhelper.SharedLogger(t), config.NewLocator(cfg))
	require.NoError(t, err)

	quarantinedRepo := localrepo.NewTestRepo(t, cfg, quarantine.QuarantinedRepo())
	blobID, err := quarantinedRepo.WriteBlob(ctx, strings.NewReader("allyourbasearebelongtous"), localrepo.WriteBlobConfig{})
	require.NoError(t, err)

	hookManager := NewManager(cfg, config.NewLocator(cfg), testhelper.SharedLogger(t), gittest.NewCommandFactory(t, cfg), nil, gitlab.NewMockClient(
		t, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive,
	), NewTransactionRegistry(storagemgr.NewTransactionRegistry()))

	//nolint:gitaly-linters
	gittest.WriteCustomHook(t, repoPath, "update", []byte(fmt.Sprintf(
		`#!/bin/sh
		git cat-file -p '%s' || true
	`, blobID.String())))

	for repo, isQuarantined := range map[*gitalypb.Repository]bool{
		quarantine.QuarantinedRepo(): true,
		repoProto:                    false,
	} {
		t.Run(fmt.Sprintf("quarantined: %v", isQuarantined), func(t *testing.T) {
			env, err := git.NewHooksPayload(
				cfg,
				repo,
				gittest.DefaultObjectHash,
				nil,
				&git.UserDetails{
					UserID:   "1234",
					Username: "user",
					Protocol: "web",
				},
				git.PreReceiveHook,
				featureflag.FromContext(ctx),
				storage.ExtractTransactionID(ctx),
			).Env()
			require.NoError(t, err)

			var stdout, stderr bytes.Buffer
			require.NoError(t, hookManager.UpdateHook(ctx, repo, "refs/heads/master",
				gittest.DefaultObjectHash.ZeroOID.String(), gittest.DefaultObjectHash.ZeroOID.String(), []string{env}, &stdout, &stderr))

			if isQuarantined {
				require.Equal(t, "allyourbasearebelongtous", stdout.String())
				require.Empty(t, stderr.String())
			} else {
				require.Empty(t, stdout.String())
				require.Contains(t, stderr.String(), "Not a valid object name")
			}
		})
	}
}
