//go:build !gitaly_test_sha256

package metadata

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestOutgoingToIncoming(t *testing.T) {
	ctx := testhelper.Context(t)

	ctx, err := storage.InjectGitalyServers(ctx, "a", "b", "c")
	require.NoError(t, err)

	_, err = storage.ExtractGitalyServer(ctx, "a")
	require.Equal(t, errors.New("empty gitaly-servers metadata"), err,
		"server should not be found in the incoming context")

	ctx = OutgoingToIncoming(ctx)

	info, err := storage.ExtractGitalyServer(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, storage.ServerInfo{Address: "b", Token: "c"}, info)
}
