package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepository_Clone(t *testing.T) {
	src := Repository{
		RelativePath: "a/b",
		Primary: Node{
			Storage: "s1",
			Address: "0.0.0.0",
			Token:   "$ecret",
		},
		Replicas: []Node{
			{
				Storage: "s2",
				Address: "0.0.0.1",
				Token:   "$ecret",
			},
			{
				Storage: "s3",
				Address: "0.0.0.2",
				Token:   "$ecret",
			},
		},
	}
	srcCp := src

	clone := src.Clone()
	require.Equal(t, src, clone)

	clone.Replicas[0].Address = "0.0.0.3"
	require.NotEqual(t, src, clone)
	require.Equal(t, srcCp, src)
}
