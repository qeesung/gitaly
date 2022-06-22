package duration

import (
	"encoding"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDuration(t *testing.T) {
	t.Parallel()

	require.Implements(t, (*encoding.TextMarshaler)(nil), new(Duration))
	require.Implements(t, (*encoding.TextUnmarshaler)(nil), new(Duration))
}
