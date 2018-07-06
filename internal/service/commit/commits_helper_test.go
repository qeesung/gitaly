package commit

import (
	"testing"

	"github.com/stretchr/testify/require"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

func TestCommitSize(t *testing.T) {
	testCases := []struct {
		desc   string
		commit *pb.GitCommit
		len    int
	}{
		{desc: "empty GitCommit", commit: &pb.GitCommit{}, len: 16},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.len, commitSize(tc.commit))
		})
	}
}
