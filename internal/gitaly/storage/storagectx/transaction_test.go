package storagectx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type nilTransaction struct {
	// Embed an integer value as Go allows the address of empty structs to be the same.
	// The test case asserts that the specific instance of a transaction is passed to the
	// callback.
	int //nolint:unused
}

func (nilTransaction) MarkDefaultBranchUpdated() {}

func (nilTransaction) DeleteRepository() {}

func (nilTransaction) IncludeObject(git.ObjectID) {}

func (nilTransaction) MarkCustomHooksUpdated() {}

func (nilTransaction) OriginalRepository(*gitalypb.Repository) *gitalypb.Repository {
	panic("unexpected call")
}

func (nilTransaction) UpdateAlternate(string) {}

func TestContextWithTransaction(t *testing.T) {
	t.Run("no transaction in context", func(t *testing.T) {
		RunWithTransaction(context.Background(), func(tx Transaction) {
			t.Fatalf("callback should not be executed without transaction")
		})
	})

	t.Run("transaction in context", func(t *testing.T) {
		callbackRan := false
		expectedTX := &nilTransaction{}

		RunWithTransaction(
			ContextWithTransaction(context.Background(), expectedTX),
			func(tx Transaction) {
				require.Same(t, expectedTX, tx)
				require.NotSame(t, tx, &nilTransaction{})
				callbackRan = true
			},
		)

		require.True(t, callbackRan)
	})
}
