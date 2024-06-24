package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestPrintAlert(t *testing.T) {
	testCases := []struct {
		message  string
		expected string
	}{
		{
			message: "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Curabitur nec mi lectus. Fusce eu ligula in odio hendrerit posuere. Ut semper neque vitae maximus accumsan. In malesuada justo nec leo congue egestas. Vivamus interdum nec libero ac convallis. Praesent euismod et nunc vitae vulputate. Mauris tincidunt ligula urna, bibendum vestibulum sapien luctus eu. Donec sed justo in erat dictum semper. Ut porttitor augue in felis gravida scelerisque. Morbi dolor justo, accumsan et nulla vitae, luctus consectetur est. Donec aliquet erat pellentesque suscipit elementum. Cras posuere eros ipsum, a tincidunt tortor laoreet quis. Mauris varius nulla vitae placerat imperdiet. Vivamus ut ligula odio. Cras nec euismod ligula.",
			expected: `========================================================================

   Lorem ipsum dolor sit amet, consectetur adipiscing elit. Curabitur
  nec mi lectus. Fusce eu ligula in odio hendrerit posuere. Ut semper
    neque vitae maximus accumsan. In malesuada justo nec leo congue
  egestas. Vivamus interdum nec libero ac convallis. Praesent euismod
    et nunc vitae vulputate. Mauris tincidunt ligula urna, bibendum
  vestibulum sapien luctus eu. Donec sed justo in erat dictum semper.
  Ut porttitor augue in felis gravida scelerisque. Morbi dolor justo,
  accumsan et nulla vitae, luctus consectetur est. Donec aliquet erat
      pellentesque suscipit elementum. Cras posuere eros ipsum, a
   tincidunt tortor laoreet quis. Mauris varius nulla vitae placerat
      imperdiet. Vivamus ut ligula odio. Cras nec euismod ligula.

========================================================================`,
		},
		{
			message: "Lorem ipsum dolor sit amet, consectetur",
			expected: `========================================================================

                Lorem ipsum dolor sit amet, consectetur

========================================================================`,
		},
	}

	for _, tc := range testCases {
		var result bytes.Buffer

		require.NoError(t, printAlert(gitlab.PostReceiveMessage{Message: tc.message}, &result))
		assert.Equal(t, tc.expected, result.String())
	}
}

func TestPostReceive_customHook(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	locator := config.NewLocator(cfg)

	txManager := transaction.NewTrackingManager()
	hookManager := NewManager(cfg, locator, testhelper.SharedLogger(t), gitCmdFactory, txManager, gitlab.NewMockClient(
		t, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive,
	), NewTransactionRegistry(storagemgr.NewTransactionRegistry()), NewProcReceiveRegistry(), nil)

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
		git.PostReceiveHook,
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
		git.PostReceiveHook,
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
		git.PostReceiveHook,
		featureflag.FromContext(ctx),
		storage.ExtractTransactionID(ctx),
	).Env()
	require.NoError(t, err)

	testCases := []struct {
		desc           string
		env            []string
		pushOptions    []string
		hook           string
		stdin          string
		expectedErr    string
		expectedStdout string
		expectedStderr string
		expectedVotes  []transaction.PhasedVote
	}{
		{
			desc:           "hook receives environment variables",
			env:            []string{payload},
			stdin:          "changes\n",
			hook:           "#!/bin/sh\nenv | grep -v -e '^SHLVL=' -e '^_=' | sort\n",
			expectedStdout: strings.Join(getExpectedEnv(t, ctx, locator, gitCmdFactory, repo), "\n") + "\n",
			expectedVotes:  []transaction.PhasedVote{},
		},
		{
			desc:  "push options are passed through",
			env:   []string{payload},
			stdin: "changes\n",
			pushOptions: []string{
				"mr.merge_when_pipeline_succeeds",
				"mr.create",
			},
			hook: "#!/bin/sh\nenv | grep -e '^GIT_PUSH_OPTION' | sort\n",
			expectedStdout: strings.Join([]string{
				"GIT_PUSH_OPTION_0=mr.merge_when_pipeline_succeeds",
				"GIT_PUSH_OPTION_1=mr.create",
				"GIT_PUSH_OPTION_COUNT=2",
			}, "\n") + "\n",
			expectedVotes: []transaction.PhasedVote{},
		},
		{
			desc:           "hook can write to stderr and stdout",
			env:            []string{payload},
			stdin:          "changes\n",
			hook:           "#!/bin/sh\necho foo >&1 && echo bar >&2\n",
			expectedStdout: "foo\n",
			expectedStderr: "bar\n",
			expectedVotes:  []transaction.PhasedVote{},
		},
		{
			desc:           "hook receives standard input",
			env:            []string{payload},
			hook:           "#!/bin/sh\ncat\n",
			stdin:          "foo\n",
			expectedStdout: "foo\n",
			expectedVotes:  []transaction.PhasedVote{},
		},
		{
			desc:           "hook succeeds without consuming stdin",
			env:            []string{payload},
			hook:           "#!/bin/sh\necho foo\n",
			stdin:          "ignore me\n",
			expectedStdout: "foo\n",
			expectedVotes:  []transaction.PhasedVote{},
		},
		{
			desc:          "invalid hook results in error",
			env:           []string{payload},
			stdin:         "changes\n",
			hook:          "",
			expectedErr:   "exec format error",
			expectedVotes: []transaction.PhasedVote{},
		},
		{
			desc:          "failing hook results in error",
			env:           []string{payload},
			stdin:         "changes\n",
			hook:          "#!/bin/sh\nexit 123",
			expectedErr:   "exit status 123",
			expectedVotes: []transaction.PhasedVote{},
		},
		{
			desc:           "hook is executed on primary",
			env:            []string{primaryPayload},
			stdin:          "changes\n",
			hook:           "#!/bin/sh\necho foo\n",
			expectedStdout: "foo\n",
			expectedVotes:  []transaction.PhasedVote{synchronizedVote("post-receive")},
		},
		{
			desc:          "hook is not executed on secondary",
			env:           []string{secondaryPayload},
			stdin:         "changes\n",
			hook:          "#!/bin/sh\necho foo\n",
			expectedVotes: []transaction.PhasedVote{synchronizedVote("post-receive")},
		},
		{
			desc:          "missing changes cause error",
			env:           []string{payload},
			expectedErr:   "hook got no reference updates",
			expectedVotes: []transaction.PhasedVote{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			txManager.Reset()

			gittest.WriteCustomHook(t, repoPath, "post-receive", []byte(tc.hook))

			var stdout, stderr bytes.Buffer
			err = hookManager.PostReceiveHook(ctx, repo, tc.pushOptions, tc.env, strings.NewReader(tc.stdin), &stdout, &stderr)

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

type postreceiveAPIMock struct {
	postreceive func(context.Context, string, string, string, ...string) (bool, []gitlab.PostReceiveMessage, error)
}

func (m *postreceiveAPIMock) Allowed(ctx context.Context, params gitlab.AllowedParams) (bool, string, error) {
	return true, "", nil
}

func (m *postreceiveAPIMock) PreReceive(ctx context.Context, glRepository string) (bool, error) {
	return true, nil
}

func (m *postreceiveAPIMock) Check(ctx context.Context) (*gitlab.CheckInfo, error) {
	return nil, errors.New("unexpected call")
}

func (m *postreceiveAPIMock) PostReceive(ctx context.Context, glRepository, glID, changes string, pushOptions ...string) (bool, []gitlab.PostReceiveMessage, error) {
	return m.postreceive(ctx, glRepository, glID, changes, pushOptions...)
}

func TestPostReceive_gitlab(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	payload, err := git.NewHooksPayload(
		cfg,
		repo,
		gittest.DefaultObjectHash,
		nil,
		&git.UserDetails{
			UserID:   "1234",
			Username: "user",
			Protocol: "web",
		}, git.PostReceiveHook, nil, storage.ExtractTransactionID(ctx)).Env()
	require.NoError(t, err)

	standardEnv := []string{payload}

	testCases := []struct {
		desc           string
		env            []string
		pushOptions    []string
		changes        string
		postreceive    func(*testing.T, context.Context, string, string, string, ...string) (bool, []gitlab.PostReceiveMessage, error)
		expectHookCall bool
		expectedErr    error
		expectedStdout string
		expectedStderr string
	}{
		{
			desc:    "allowed change",
			env:     standardEnv,
			changes: "changes\n",
			postreceive: func(t *testing.T, ctx context.Context, glRepo, glID, changes string, pushOptions ...string) (bool, []gitlab.PostReceiveMessage, error) {
				require.Equal(t, repo.GlRepository, glRepo)
				require.Equal(t, "1234", glID)
				require.Equal(t, "changes\n", changes)
				require.Empty(t, pushOptions)
				return true, nil, nil
			},
			expectedStdout: "hook called\n",
		},
		{
			desc: "push options are passed through",
			env:  standardEnv,
			pushOptions: []string{
				"mr.merge_when_pipeline_succeeds",
				"mr.create",
			},
			changes: "changes\n",
			postreceive: func(t *testing.T, ctx context.Context, glRepo, glID, changes string, pushOptions ...string) (bool, []gitlab.PostReceiveMessage, error) {
				require.Equal(t, []string{
					"mr.merge_when_pipeline_succeeds",
					"mr.create",
				}, pushOptions)
				return true, nil, nil
			},
			expectedStdout: "hook called\n",
		},
		{
			desc:    "access denied without message",
			env:     standardEnv,
			changes: "changes\n",
			postreceive: func(t *testing.T, ctx context.Context, glRepo, glID, changes string, pushOptions ...string) (bool, []gitlab.PostReceiveMessage, error) {
				return false, nil, nil
			},
			expectedErr: errors.New(""),
		},
		{
			desc:    "access denied with message",
			env:     standardEnv,
			changes: "changes\n",
			postreceive: func(t *testing.T, ctx context.Context, glRepo, glID, changes string, pushOptions ...string) (bool, []gitlab.PostReceiveMessage, error) {
				return false, []gitlab.PostReceiveMessage{
					{
						Message: "access denied",
						Type:    "alert",
					},
				}, nil
			},
			expectedStdout: "\n========================================================================\n\n                             access denied\n\n========================================================================\n\n",
			expectedErr:    errors.New(""),
		},
		{
			desc:    "access check returns error",
			env:     standardEnv,
			changes: "changes\n",
			postreceive: func(t *testing.T, ctx context.Context, glRepo, glID, changes string, pushOptions ...string) (bool, []gitlab.PostReceiveMessage, error) {
				return false, nil, errors.New("failure")
			},
			expectedErr: fmt.Errorf("GitLab: %w", fmt.Errorf("failure")),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			gitlabAPI := postreceiveAPIMock{
				postreceive: func(ctx context.Context, glRepo, glID, changes string, pushOptions ...string) (bool, []gitlab.PostReceiveMessage, error) {
					return tc.postreceive(t, ctx, glRepo, glID, changes, pushOptions...)
				},
			}

			hookManager := NewManager(
				cfg,
				config.NewLocator(cfg),
				testhelper.SharedLogger(t),
				gittest.NewCommandFactory(t, cfg),
				transaction.NewManager(cfg, testhelper.SharedLogger(t), backchannel.NewRegistry()),
				&gitlabAPI, NewTransactionRegistry(storagemgr.NewTransactionRegistry()),
				NewProcReceiveRegistry(),
				nil,
			)

			gittest.WriteCustomHook(t, repoPath, "post-receive", []byte("#!/bin/sh\necho hook called\n"))

			var stdout, stderr bytes.Buffer
			err = hookManager.PostReceiveHook(ctx, repo, tc.pushOptions, tc.env, strings.NewReader(tc.changes), &stdout, &stderr)

			if tc.expectedErr != nil {
				require.Equal(t, tc.expectedErr, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.expectedStdout, stdout.String())
			require.Equal(t, tc.expectedStderr, stderr.String())
		})
	}
}

func TestPostReceive_quarantine(t *testing.T) {
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
	), NewTransactionRegistry(storagemgr.NewTransactionRegistry()), NewProcReceiveRegistry(), nil)

	gittest.WriteCustomHook(t, repoPath, "post-receive", []byte(fmt.Sprintf(
		`#!/bin/sh
		git cat-file -p %q || true
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

			stdin := strings.NewReader(fmt.Sprintf("%s %s refs/heads/master",
				gittest.DefaultObjectHash.ZeroOID, gittest.DefaultObjectHash.ZeroOID))

			var stdout, stderr bytes.Buffer
			require.NoError(t, hookManager.PostReceiveHook(ctx, repo, nil,
				[]string{env}, stdin, &stdout, &stderr))

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
