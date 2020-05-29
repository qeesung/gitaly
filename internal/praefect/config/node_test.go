package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNode_MarshalJSON(t *testing.T) {
	token := "secretToken"
	node := &Node{
		Storage:        "storage",
		Address:        "address",
		Token:          token,
		DefaultPrimary: true,
	}

	b, err := json.Marshal(node)
	require.NoError(t, err)
	require.JSONEq(t, `{"storage":"storage","address":"address","primary":true}`, string(b))
}
