package git_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
)

func TestVersionComparator(t *testing.T) {
	v2s := []string{
		"0.0.0", // 0
		"0.0.1", // 1
		"0.1.0", // 2
		"0.1.1", // 3
		"1.0.0", // 4
		"1.0.1", // 5
		"1.1.0", // 6
		"1.1.1", // 7
	}

	for v1, expects := range map[string][]int{
		//       v2:   0, 1, 2, 3, 4, 5, 6, 7
		"0.0.0": []int{0, 1, 1, 1, 1, 1, 1, 1}, // 0 is false, 1 is true
		"0.0.1": []int{0, 0, 1, 1, 1, 1, 1, 1},
		"0.1.0": []int{0, 0, 0, 1, 1, 1, 1, 1},
		"0.1.1": []int{0, 0, 0, 0, 1, 1, 1, 1},
		"1.0.0": []int{0, 0, 0, 0, 0, 1, 1, 1},
		"1.0.1": []int{0, 0, 0, 0, 0, 0, 1, 1},
		"1.1.0": []int{0, 0, 0, 0, 0, 0, 0, 1},
		"1.1.1": []int{0, 0, 0, 0, 0, 0, 0, 0},
	} {
		for i, expectInt := range expects {
			v2 := v2s[i]             // look up 2nd version string
			expect := expectInt == 1 // convert int to expected bool

			actual, err := git.VersionLessThan(v1, v2)
			require.NoError(t, err)
			require.Equal(t, expect, actual)
		}
	}
}
