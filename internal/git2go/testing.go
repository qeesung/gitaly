package git2go

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
)

// CommitAssertion is a helper struct for asserting the equality of two commits.
type CommitAssertion struct {
	Parent    string
	Author    Signature
	Committer Signature
	Message   string
}

// GetCommitAssertion is a test helper for parsing a commit in to an object that can be used for equality
// assertions. Parsing functionality is incomplete.
func GetCommitAssertion(ctx context.Context, t testing.TB, repo *localrepo.Repo, oid string) CommitAssertion {
	t.Helper()

	data, err := repo.ReadObject(ctx, oid)
	require.NoError(t, err)

	var commit CommitAssertion
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if line == "" {
			commit.Message = strings.Join(lines[i+1:], "\n")
			break
		}

		split := strings.SplitN(line, " ", 2)
		require.Len(t, split, 2, "invalid commit: %q", data)

		field, value := split[0], split[1]
		switch field {
		case "parent":
			require.Empty(t, commit.Parent, "multi parent parsing not implemented")
			commit.Parent = value
		case "author":
			require.Empty(t, commit.Author, "commit contained multiple authors")
			commit.Author = unmarshalSignature(t, value)
		case "committer":
			require.Empty(t, commit.Committer, "commit contained multiple committers")
			commit.Committer = unmarshalSignature(t, value)
		default:
		}
	}

	return commit
}

func unmarshalSignature(t testing.TB, data string) Signature {
	t.Helper()

	// Format: NAME <EMAIL> DATE_UNIX DATE_TIMEZONE
	split1 := strings.Split(data, " <")
	require.Len(t, split1, 2, "invalid signature: %q", data)

	split2 := strings.Split(split1[1], "> ")
	require.Len(t, split2, 2, "invalid signature: %q", data)

	split3 := strings.Split(split2[1], " ")
	require.Len(t, split3, 2, "invalid signature: %q", data)

	timestamp, err := strconv.ParseInt(split3[0], 10, 64)
	require.NoError(t, err)

	return Signature{
		Name:  split1[0],
		Email: split2[0],
		When:  time.Unix(timestamp, 0).UTC(),
	}
}
