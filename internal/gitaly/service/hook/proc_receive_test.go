package hook

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestProcReceiveHook(t *testing.T) {
	procReceiveRegistry := gitalyhook.NewProcReceiveRegistry()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runHooksServer(t, cfg, nil, testserver.WithProcReceiveRegistry(procReceiveRegistry))
	ctx := testhelper.Context(t)
	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	clientConn := func(
		t *testing.T,
		transactionID storage.TransactionID,
		steps []func(bytes.Buffer) *gitalypb.ProcReceiveHookResponse,
	) {
		hooksPayload, err := git.NewHooksPayload(
			cfg,
			repo,
			gittest.DefaultObjectHash,
			nil,
			nil,
			git.ProcReceiveHook,
			featureflag.FromContext(ctx),
			transactionID,
		).Env()
		assert.NoError(t, err)

		client, conn := newHooksClient(t, cfg.SocketPath)
		defer conn.Close()

		stream, err := client.ProcReceiveHook(ctx)
		assert.NoError(t, err)

		assert.NoError(t, stream.Send(&gitalypb.ProcReceiveHookRequest{
			EnvironmentVariables: []string{hooksPayload},
			Repository:           repo,
		}))

		ref := git.ReferenceName(fmt.Sprintf("refs/heads/main_%d", transactionID))

		var stdin bytes.Buffer
		_, err = pktline.WriteString(&stdin, "version=1\000atomic")
		assert.NoError(t, err)
		err = pktline.WriteFlush(&stdin)
		assert.NoError(t, err)
		_, err = pktline.WriteString(&stdin, fmt.Sprintf("%s %s %s",
			gittest.DefaultObjectHash.ZeroOID, gittest.DefaultObjectHash.EmptyTreeOID, ref))
		assert.NoError(t, err)
		err = pktline.WriteFlush(&stdin)
		assert.NoError(t, err)

		assert.NoError(t, stream.Send(&gitalypb.ProcReceiveHookRequest{
			Stdin: stdin.Bytes(),
		}))

		var buf bytes.Buffer
		for _, f := range steps {
			expectedResp := f(buf)

			resp, err := stream.Recv()
			assert.NoError(t, err)

			testhelper.ProtoEqualAssert(t, expectedResp, resp)

			buf.Reset()
		}
	}

	registerWaiter := func(t *testing.T, transactionID storage.TransactionID) (<-chan gitalyhook.ProcReceiveHandler, func()) {
		recvCh, cleanup, err := procReceiveRegistry.RegisterWaiter(transactionID)
		assert.NoError(t, err)
		return recvCh, cleanup
	}

	for _, tc := range []struct {
		desc               string
		number             int
		clientSteps        func(t *testing.T, transactionID storage.TransactionID) []func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse
		mockReceivePackRPC func(t *testing.T, recvCh <-chan gitalyhook.ProcReceiveHandler, transactionID storage.TransactionID)
	}{
		{
			desc:   "multiple transactions",
			number: 10,
			clientSteps: func(t *testing.T, transactionID storage.TransactionID) []func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
				ref := git.ReferenceName(fmt.Sprintf("refs/heads/main_%d", transactionID))

				return []func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse{
					func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
						_, err := pktline.WriteString(&buf, "version=1\000atomic")
						assert.NoError(t, err)

						return &gitalypb.ProcReceiveHookResponse{
							Stdout: buf.Bytes(),
						}
					},
					func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
						err := pktline.WriteFlush(&buf)
						assert.NoError(t, err)

						return &gitalypb.ProcReceiveHookResponse{
							Stdout: buf.Bytes(),
						}
					},
					func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
						_, err := pktline.WriteString(&buf, "ok "+ref.String())
						assert.NoError(t, err)

						return &gitalypb.ProcReceiveHookResponse{
							Stdout: buf.Bytes(),
						}
					},
					func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
						err := pktline.WriteFlush(&buf)
						assert.NoError(t, err)

						return &gitalypb.ProcReceiveHookResponse{
							Stdout: buf.Bytes(),
						}
					},
				}
			},
			mockReceivePackRPC: func(t *testing.T, recvCh <-chan gitalyhook.ProcReceiveHandler, transactionID storage.TransactionID) {
				transmitter := <-recvCh

				ref := git.ReferenceName(fmt.Sprintf("refs/heads/main_%d", transactionID))

				assert.True(t, transmitter.Atomic())
				assert.Equal(t, []gitalyhook.ReferenceUpdate{{
					Ref:    ref,
					OldOID: gittest.DefaultObjectHash.ZeroOID,
					NewOID: gittest.DefaultObjectHash.EmptyTreeOID,
				}}, transmitter.ReferenceUpdates())

				err := transmitter.AcceptUpdate(ref)
				assert.NoError(t, err)

				err = transmitter.Close(nil)
				assert.NoError(t, err)
			},
		},
		{
			desc:   "client failure",
			number: 1,
			clientSteps: func(t *testing.T, transactionID storage.TransactionID) []func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
				return []func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse{
					func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
						_, err := pktline.WriteString(&buf, "version=1\000atomic")
						assert.NoError(t, err)

						return &gitalypb.ProcReceiveHookResponse{
							Stdout: buf.Bytes(),
						}
					},
					func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
						err := pktline.WriteFlush(&buf)
						assert.NoError(t, err)

						return &gitalypb.ProcReceiveHookResponse{
							Stdout: buf.Bytes(),
							Stderr: nil,
						}
					},
					func(buf bytes.Buffer) *gitalypb.ProcReceiveHookResponse {
						return &gitalypb.ProcReceiveHookResponse{
							Stdout:     nil,
							Stderr:     []byte("handler finished: don't need reason"),
							ExitStatus: &gitalypb.ExitStatus{Value: 1},
						}
					},
				}
			},
			mockReceivePackRPC: func(t *testing.T, recvCh <-chan gitalyhook.ProcReceiveHandler, transactionID storage.TransactionID) {
				transmitter := <-recvCh

				ref := git.ReferenceName(fmt.Sprintf("refs/heads/main_%d", transactionID))

				assert.True(t, transmitter.Atomic())
				assert.Equal(t, []gitalyhook.ReferenceUpdate{{
					Ref:    ref,
					OldOID: gittest.DefaultObjectHash.ZeroOID,
					NewOID: gittest.DefaultObjectHash.EmptyTreeOID,
				}}, transmitter.ReferenceUpdates())

				err := transmitter.Close(errors.New("don't need reason"))
				assert.NoError(t, err)
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			var wg sync.WaitGroup

			for i := 1; i <= tc.number; i++ {
				id := storage.TransactionID(i)

				recvCh, cleanup := registerWaiter(t, id)

				wg.Add(1)
				go func() {
					defer wg.Done()
					tc.mockReceivePackRPC(t, recvCh, id)
					cleanup()
				}()

				wg.Add(1)
				go func() {
					defer wg.Done()
					clientConn(t, id, tc.clientSteps(t, id))
				}()
			}

			wg.Wait()
		})
	}
}
