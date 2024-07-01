package hook

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestProcReceiveRegistry(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	receiveHooksPayload := &git.UserDetails{
		UserID:   "1234",
		Username: "user",
		Protocol: "web",
	}
	repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	newHandler := func(id storage.TransactionID) (ProcReceiveHandler, <-chan error) {
		payload, err := git.NewHooksPayload(
			cfg,
			repo,
			gittest.DefaultObjectHash,
			nil,
			receiveHooksPayload,
			git.PreReceiveHook,
			featureflag.FromContext(ctx),
			id,
		).Env()
		require.NoError(t, err)

		var stdin bytes.Buffer
		_, err = pktline.WriteString(&stdin, "version=1\000push-options atomic")
		require.NoError(t, err)
		err = pktline.WriteFlush(&stdin)
		require.NoError(t, err)
		_, err = pktline.WriteString(&stdin, fmt.Sprintf("%s %s %s",
			gittest.DefaultObjectHash.ZeroOID, gittest.DefaultObjectHash.EmptyTreeOID, "refs/heads/main"))
		require.NoError(t, err)
		err = pktline.WriteFlush(&stdin)
		require.NoError(t, err)

		var stdout, stderr bytes.Buffer

		handler, doneCh, err := NewProcReceiveHandler([]string{payload}, &stdin, &stdout, &stderr)
		require.NoError(t, err)

		return handler, doneCh
	}

	t.Run("transmit called before register", func(t *testing.T) {
		t.Parallel()
		registry := NewProcReceiveRegistry()

		handler, _ := newHandler(1)
		err := registry.Transmit(ctx, handler)

		require.Equal(t, fmt.Errorf("no waiters for id: 1"), err)
	})

	t.Run("transmit with context cancelled", func(t *testing.T) {
		t.Parallel()
		registry := NewProcReceiveRegistry()

		handler, _ := newHandler(1)

		recvCh, cleanup, err := registry.RegisterWaiter(1)
		require.NoError(t, err)
		defer cleanup()

		ctx, cancel := context.WithCancel(ctx)
		cancel()

		wg := sync.WaitGroup{}
		wg.Add(2)

		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				assert.Equal(t, context.Canceled, ctx.Err())
			case <-recvCh:
				assert.Fail(t, "handler wasn't expected")
			}
		}()

		go func() {
			defer wg.Done()
			err := registry.Transmit(ctx, handler)
			assert.Equal(t, context.Canceled, err)
		}()

		wg.Wait()
	})

	t.Run("waiter registered twice", func(t *testing.T) {
		t.Parallel()
		registry := NewProcReceiveRegistry()

		_, _, err := registry.RegisterWaiter(1)
		require.NoError(t, err)

		_, _, err = registry.RegisterWaiter(1)
		require.Equal(t, fmt.Errorf("cannot register id: 1 again"), err)
	})

	t.Run("multiple handlers", func(t *testing.T) {
		t.Parallel()

		registry := NewProcReceiveRegistry()

		handler1, _ := newHandler(1)
		handler2, _ := newHandler(2)
		handler3, _ := newHandler(3)

		recvCh1, cleanup, err := registry.RegisterWaiter(1)
		require.NoError(t, err)
		defer cleanup()

		recvCh2, cleanup2, err := registry.RegisterWaiter(2)
		require.NoError(t, err)
		defer cleanup2()

		recvCh3, cleanup3, err := registry.RegisterWaiter(3)
		require.NoError(t, err)
		defer cleanup3()

		wg := sync.WaitGroup{}
		wg.Add(6)

		go func() {
			defer wg.Done()
			handlerObtained := <-recvCh1
			assert.Equal(t, handler1, handlerObtained)
		}()

		go func() {
			defer wg.Done()
			handlerObtained := <-recvCh2
			assert.Equal(t, handler2, handlerObtained)
		}()

		go func() {
			defer wg.Done()
			handlerObtained := <-recvCh3
			assert.Equal(t, handler3, handlerObtained)
		}()

		go func() {
			defer wg.Done()
			assert.NoError(t, registry.Transmit(ctx, handler1))
		}()

		go func() {
			defer wg.Done()
			assert.NoError(t, registry.Transmit(ctx, handler2))
		}()

		go func() {
			defer wg.Done()
			assert.NoError(t, registry.Transmit(ctx, handler3))
		}()

		wg.Wait()
	})
}
