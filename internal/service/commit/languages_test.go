package commit

import (
	"context"
	"testing"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/stretchr/testify/require"
)

func TestLanguages(t *testing.T) {
	client := newCommitServiceClient(t)
	request := &pb.CommitLanguagesRequest{
		Repository: testRepo,
		Revision:   []byte("e63f41fe459e62e1228fcef60d7189127aeba95a"),
	}

	resp, err := client.CommitLanguages(context.Background(), request)
	require.NoError(t, err)

	require.NotZero(t, len(resp.Languages), "number of languages in response")

	expectedLanguages := []pb.CommitLanguagesResponse_Language{
		{Name: "Ruby", Share: 66, Color: "#701516"},
		{Name: "JavaScript", Share: 22, Color: "#f1e05a"},
		{Name: "HTML", Share: 7, Color: "#e44b23"},
		{Name: "CoffeeScript", Share: 2, Color: "#244776"},
	}

	for i, el := range expectedLanguages {
		actualLanguage := resp.Languages[i]
		require.True(t, languageEqual(&el, actualLanguage), "language %+v not equal to %+v", el, *actualLanguage)
	}
}

func languageEqual(expected, actual *pb.CommitLanguagesResponse_Language) bool {
	if expected.Name != actual.Name {
		return false
	}
	if expected.Color != actual.Color {
		return false
	}
	if (expected.Share-actual.Share)*(expected.Share-actual.Share) >= 1.0 {
		return false
	}
	return true
}

func TestLanguagesEmptyRevision(t *testing.T) {
	client := newCommitServiceClient(t)
	request := &pb.CommitLanguagesRequest{
		Repository: testRepo,
	}

	resp, err := client.CommitLanguages(context.Background(), request)
	require.NoError(t, err)

	require.NotZero(t, len(resp.Languages), "number of languages in response")

	foundRuby := false
	for _, l := range resp.Languages {
		if l.Name == "Ruby" {
			foundRuby = true
		}
	}
	require.True(t, foundRuby, "expected to find Ruby as a language on HEAD")
}
