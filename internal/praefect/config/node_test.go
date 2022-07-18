//go:build !gitaly_test_sha256

package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNode_MarshalJSON(t *testing.T) {
	token := "secretToken"
	node := &Node{
		Storage: "storage",
		Address: "address",
		Token:   token,
	}

	b, err := json.Marshal(node)
	require.NoError(t, err)
	require.JSONEq(t, `{"storage":"storage","address":"address"}`, string(b))
}
