package objectpool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func NewTestObjectPool(ctx context.Context, t *testing.T, storageName string) (*ObjectPool, func()) {
	pool, err := NewObjectPool(storageName, NewObjectPoolName(t))
	require.NoError(t, err)
	return pool, func() {
		require.NoError(t, pool.Remove(ctx))
	}
}
