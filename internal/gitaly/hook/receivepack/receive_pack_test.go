package receivepack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRegisterProcReceiveHook(t *testing.T) {
	ctx := testhelper.Context(t)
	logger := testhelper.SharedLogger(t)
	cfg := testcfg.Build(t)

	noopCommit := func(_ context.Context) error {
		return nil
	}

	type setupData struct {
		repoProto               *gitalypb.Repository
		pktLineRequest          string
		updateHook              func(t *testing.T, ctx context.Context, repo *gitalypb.Repository, ref, oldValue, newValue string, env []string, stdout, stderr io.Writer) error
		postReceiveHook         func(t *testing.T, ctx context.Context, repo *gitalypb.Repository, pushOptions, env []string, stdin io.Reader, stdout, stderr io.Writer) error
		commit                  func(ctx context.Context) error
		expectedRefs            []git.Reference
		expectedPktLineResponse string
		expectedStdout          string
		expectedStderr          string
		expectedErrMsg          string
	}

	for _, tc := range []struct {
		desc  string
		setup func() setupData
	}{
		{
			desc: "successful atomic single update",
			setup: func() setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				var pktLineRequest bytes.Buffer
				_, err := pktline.WriteString(&pktLineRequest, "version=1\000atomic")
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/main"))
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)

				return setupData{
					repoProto:               repo,
					pktLineRequest:          pktLineRequest.String(),
					updateHook:              hook.NopUpdate,
					postReceiveHook:         hook.NopPostReceive,
					commit:                  noopCommit,
					expectedPktLineResponse: "0014version=1\000atomic00000016ok refs/heads/main0000",
					expectedRefs: []git.Reference{
						{
							Name:   "refs/heads/main",
							Target: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "successful non-atomic single update",
			setup: func() setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				var pktLineRequest bytes.Buffer
				_, err := pktline.WriteString(&pktLineRequest, "version=1\n")
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/main"))
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)

				return setupData{
					repoProto:               repo,
					pktLineRequest:          pktLineRequest.String(),
					updateHook:              hook.NopUpdate,
					postReceiveHook:         hook.NopPostReceive,
					commit:                  noopCommit,
					expectedPktLineResponse: "000dversion=100000016ok refs/heads/main0000",
					expectedRefs: []git.Reference{
						{
							Name:   "refs/heads/main",
							Target: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "successful atomic multiple updates",
			setup: func() setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				var pktLineRequest bytes.Buffer
				_, err := pktline.WriteString(&pktLineRequest, "version=1\000atomic")
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/foo"))
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/bar"))
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)

				return setupData{
					repoProto:      repo,
					pktLineRequest: pktLineRequest.String(),
					updateHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, ref, _, _ string, _ []string, stdout, _ io.Writer) error {
						_, err := fmt.Fprintf(stdout, "update hook: %s\n", ref)
						require.NoError(t, err)
						return nil
					},
					postReceiveHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
						_, err := fmt.Fprintf(stdout, "post-receive hook\n")
						require.NoError(t, err)
						return nil
					},
					commit:                  noopCommit,
					expectedStdout:          "update hook: refs/heads/foo\nupdate hook: refs/heads/bar\npost-receive hook\n",
					expectedPktLineResponse: "0014version=1\000atomic00000015ok refs/heads/foo0015ok refs/heads/bar0000",
					expectedRefs: []git.Reference{
						{
							Name:   "refs/heads/foo",
							Target: commitID.String(),
						},
						{
							Name:   "refs/heads/bar",
							Target: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "successful non-atomic multiple updates",
			setup: func() setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				var pktLineRequest bytes.Buffer
				_, err := pktline.WriteString(&pktLineRequest, "version=1\n")
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/foo"))
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/bar"))
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)

				return setupData{
					repoProto:      repo,
					pktLineRequest: pktLineRequest.String(),
					updateHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, ref, _, _ string, _ []string, stdout, _ io.Writer) error {
						_, err := fmt.Fprintf(stdout, "update hook: %s\n", ref)
						require.NoError(t, err)
						return nil
					},
					postReceiveHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
						_, err := fmt.Fprintf(stdout, "post-receive hook\n")
						require.NoError(t, err)
						return nil
					},
					commit:                  noopCommit,
					expectedStdout:          "update hook: refs/heads/foo\nupdate hook: refs/heads/bar\npost-receive hook\n",
					expectedPktLineResponse: "000dversion=100000015ok refs/heads/foo0015ok refs/heads/bar0000",
					expectedRefs: []git.Reference{
						{
							Name:   "refs/heads/foo",
							Target: commitID.String(),
						},
						{
							Name:   "refs/heads/bar",
							Target: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "update hook fails atomic update",
			setup: func() setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				var pktLineRequest bytes.Buffer
				_, err := pktline.WriteString(&pktLineRequest, "version=1\000atomic")
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/foo"))
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/bar"))
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)

				return setupData{
					repoProto:      repo,
					pktLineRequest: pktLineRequest.String(),
					updateHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, ref, _, _ string, _ []string, _, stderr io.Writer) error {
						if ref == "refs/heads/bar" {
							_, err := fmt.Fprintf(stderr, "update hook failed: %s\n", ref)
							require.NoError(t, err)
							return errors.New("update hook failed")
						}
						return nil
					},
					postReceiveHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
						_, err := fmt.Fprintf(stdout, "post-receive hook\n")
						require.NoError(t, err)
						return nil
					},
					commit:                  noopCommit,
					expectedStderr:          "update hook failed: refs/heads/bar\n",
					expectedPktLineResponse: "0014version=1\000atomic0000",
					expectedErrMsg:          "updating references atomically: running update hook: update hook failed",
				}
			},
		},
		{
			desc: "update hook fails partial non-atomic update",
			setup: func() setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				var pktLineRequest bytes.Buffer
				_, err := pktline.WriteString(&pktLineRequest, "version=1\n")
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/foo"))
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/bar"))
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)

				return setupData{
					repoProto:      repo,
					pktLineRequest: pktLineRequest.String(),
					updateHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, ref, _, _ string, _ []string, _, stderr io.Writer) error {
						if ref == "refs/heads/bar" {
							_, err := fmt.Fprintf(stderr, "update hook failed: %s\n", ref)
							require.NoError(t, err)
							return hook.NewCustomHookError(errors.New("update hook failed"))
						}
						return nil
					},
					postReceiveHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, stdin io.Reader, stdout, _ io.Writer) error {
						_, err := fmt.Fprintf(stdout, "post-receive hook\n")
						require.NoError(t, err)
						// Only accepted references should be passed to the post-receive hook.
						refs, err := io.ReadAll(stdin)
						require.NoError(t, err)
						require.Equal(t, fmt.Sprintf("%s %s refs/heads/foo\n", gittest.DefaultObjectHash.ZeroOID, commitID.String()), string(refs))
						return nil
					},
					commit:                  noopCommit,
					expectedStdout:          "post-receive hook\n",
					expectedStderr:          "update hook failed: refs/heads/bar\n",
					expectedPktLineResponse: "000dversion=100000015ok refs/heads/foo0028ng refs/heads/bar update hook failed0000",
					expectedRefs: []git.Reference{
						{
							Name:   "refs/heads/foo",
							Target: commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "commit fails",
			setup: func() setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				var pktLineRequest bytes.Buffer
				_, err := pktline.WriteString(&pktLineRequest, "version=1\000atomic")
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/main"))
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)

				return setupData{
					repoProto:       repo,
					pktLineRequest:  pktLineRequest.String(),
					updateHook:      hook.NopUpdate,
					postReceiveHook: hook.NopPostReceive,
					commit: func(ctx context.Context) error {
						return errors.New("commit failed")
					},
					// The reference gets updated, but not accepted.
					expectedPktLineResponse: "0014version=1\000atomic0000",
					expectedRefs: []git.Reference{
						{
							Name:   "refs/heads/main",
							Target: commitID.String(),
						},
					},
					expectedErrMsg: "committing transaction: commit failed",
				}
			},
		},
		{
			desc: "post-receive hook fails",
			setup: func() setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				var pktLineRequest bytes.Buffer
				_, err := pktline.WriteString(&pktLineRequest, "version=1\000atomic")
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)
				_, err = pktline.WriteString(&pktLineRequest, fmt.Sprintf("%s %s %s",
					gittest.DefaultObjectHash.ZeroOID, commitID.String(), "refs/heads/main"))
				require.NoError(t, err)
				err = pktline.WriteFlush(&pktLineRequest)
				require.NoError(t, err)

				return setupData{
					repoProto:      repo,
					pktLineRequest: pktLineRequest.String(),
					updateHook:     hook.NopUpdate,
					postReceiveHook: func(t *testing.T, _ context.Context, _ *gitalypb.Repository, _, _ []string, _ io.Reader, _, stderr io.Writer) error {
						_, err := fmt.Fprintf(stderr, "post-receive hook failed\n")
						require.NoError(t, err)
						return hook.NewCustomHookError(errors.New("post-receive hook failed"))
					},
					commit: noopCommit,
					// Errors during the post-receive hook do not result in failure.
					expectedStderr:          "post-receive hook failed\n",
					expectedPktLineResponse: "0014version=1\x00atomic00000016ok refs/heads/main0000",
					expectedRefs: []git.Reference{
						{
							Name:   "refs/heads/main",
							Target: commitID.String(),
						},
					},
				}
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			data := tc.setup()

			procReceiveRegistry := hook.NewProcReceiveRegistry()
			transactionID := storage.TransactionID(9001)
			repo := localrepo.NewTestRepo(t, cfg, data.repoProto)
			repoPath, err := repo.Path()
			require.NoError(t, err)

			var stdout, stderr bytes.Buffer
			cleanup, err := RegisterProcReceiveHook(
				ctx,
				logger,
				cfg,
				&gitalypb.PostReceivePackRequest{},
				repo,
				hook.NewMockManager(t, nil, data.postReceiveHook, data.updateHook, nil, procReceiveRegistry),
				&mockTransactionRegistry{
					getFunc: func(id storage.TransactionID) (hook.Transaction, error) {
						return mockTransaction{
							commitFunc: data.commit,
						}, nil
					},
				},
				transactionID,
				&stdout,
				&stderr,
			)
			require.NoError(t, err)

			env, err := git.NewHooksPayload(
				cfg,
				data.repoProto,
				gittest.DefaultObjectHash,
				nil,
				&git.UserDetails{},
				git.ReceivePackHooks,
				featureflag.FromContext(ctx),
				transactionID,
			).Env()
			require.NoError(t, err)

			var pktLineResponse bytes.Buffer
			handler, doneCh, err := hook.NewProcReceiveHandler(
				[]string{env}, strings.NewReader(data.pktLineRequest), &pktLineResponse, nil,
			)
			require.NoError(t, err)

			require.NoError(t, procReceiveRegistry.Transmit(ctx, handler))

			// If there is an error, doneCh and cleanup() should report the same error.
			if err := <-doneCh; err != nil && data.expectedErrMsg != "" {
				require.Equal(t, data.expectedErrMsg, err.Error())
			} else {
				require.NoError(t, err)
			}

			if err := cleanup(); err != nil && data.expectedErrMsg != "" {
				require.Equal(t, data.expectedErrMsg, err.Error())
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, data.expectedPktLineResponse, pktLineResponse.String())
			require.Equal(t, data.expectedStdout, stdout.String())
			require.Equal(t, data.expectedStderr, stderr.String())
			require.ElementsMatch(t, data.expectedRefs, gittest.GetReferences(t, cfg, repoPath))
		})
	}
}

type mockTransactionRegistry struct {
	getFunc func(storage.TransactionID) (hook.Transaction, error)
}

func (m mockTransactionRegistry) Get(id storage.TransactionID) (hook.Transaction, error) {
	return m.getFunc(id)
}

type mockTransaction struct {
	hook.Transaction
	commitFunc func(context.Context) error
}

func (m mockTransaction) Commit(ctx context.Context) error {
	return m.commitFunc(ctx)
}
