package gogit

import (
	"testing"

	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestFindCommit(t *testing.T) {
	_, repoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testCases := []struct {
		revision string
		commit   *pb.GitCommit
	}{
		{
			revision: "7975be0116940bf2ad4321f79d02a55c5f7779aa",
			commit: &pb.GitCommit{
				Id:      "7975be0116940bf2ad4321f79d02a55c5f7779aa",
				Subject: []byte("Merge branch 'rd-add-file-larger-than-1-mb' into 'master'"),
				Body:    []byte("Merge branch 'rd-add-file-larger-than-1-mb' into 'master'\n\nAdd file larger than 1 mb\n\nSee merge request gitlab-org/gitlab-test!32"),
				Author: &pb.CommitAuthor{
					Name:  []byte("Douwe Maan"),
					Email: []byte("douwe@gitlab.com"),
					Date:  &timestamp.Timestamp{Seconds: 1523259684},
				},
				Committer: &pb.CommitAuthor{
					Name:  []byte("Douwe Maan"),
					Email: []byte("douwe@gitlab.com"),
					Date:  &timestamp.Timestamp{Seconds: 1523259684},
				},
				ParentIds: []string{"60ecb67744cb56576c30214ff52294f8ce2def98", "c84ff944ff4529a70788a5e9003c2b7feae29047"},
				BodySize:  129,
			},
		},
		{
			revision: "gitaly-windows-1251",
			commit: &pb.GitCommit{
				Id:      "c809470461118b7bcab850f6e9a7ca97ac42f8ea",
				Subject: []byte("\304\356\341\340\342\350\362\374 \364\340\351\353\373 \342 \352\356\344\350\360\356\342\352\340\365 Windows-1251 \350 UTF-8"),
				Body:    []byte("\304\356\341\340\342\350\362\374 \364\340\351\353\373 \342 \352\356\344\350\360\356\342\352\340\365 Windows-1251 \350 UTF-8\n"),
				Author: &pb.CommitAuthor{
					Name:  []byte("Jacob Vosmaer"),
					Email: []byte("jacob@gitlab.com"),
					Date:  &timestamp.Timestamp{Seconds: 1512132977},
				},
				Committer: &pb.CommitAuthor{
					Name:  []byte("Jacob Vosmaer"),
					Email: []byte("jacob@gitlab.com"),
					Date:  &timestamp.Timestamp{Seconds: 1512132977},
				},
				ParentIds: []string{"e63f41fe459e62e1228fcef60d7189127aeba95a"},
				BodySize:  49,
			},
		},
		{
			revision: "gitaly-non-utf8-commit",
			commit: &pb.GitCommit{
				Id:      "0999bb770f8dc92ab5581cc0b474b3e31a96bf5c",
				Subject: []byte("Hello\360world"),
				Body:    []byte("Hello\360world\n"),
				Author: &pb.CommitAuthor{
					Name:  []byte("Jacob Vosmaer"),
					Email: []byte("jacob@gitlab.com"),
					Date:  &timestamp.Timestamp{Seconds: 1517328273},
				},
				Committer: &pb.CommitAuthor{
					Name:  []byte("Jacob Vosmaer"),
					Email: []byte("jacob@gitlab.com"),
					Date:  &timestamp.Timestamp{Seconds: 1517328273},
				},
				ParentIds: []string{"60ecb67744cb56576c30214ff52294f8ce2def98"},
				BodySize:  12,
			},
		},
		{
			revision: "author-committer-different",
			commit: &pb.GitCommit{
				Id:      "77e835ef0856f33c4f0982f84d10bdb0567fe440",
				Subject: []byte("Add file larger than 1 mb"),
				Body:    []byte("Add file larger than 1 mb\n\nIn order to test Max File Size push rule we need a file larger than 1 MB\n"),
				Author: &pb.CommitAuthor{
					Name:  []byte("Ruben Davila"),
					Email: []byte("rdavila84@gmail.com"),
					Date:  &timestamp.Timestamp{Seconds: 1523247267},
				},
				Committer: &pb.CommitAuthor{
					Name:  []byte("Jacob Vosmaer"),
					Email: []byte("jacob@gitlab.com"),
					Date:  &timestamp.Timestamp{Seconds: 1527855450},
				},
				ParentIds: []string{"60ecb67744cb56576c30214ff52294f8ce2def98"},
				BodySize:  100,
			},
		},
	}

	for _, tc := range testCases {
		foundCommit, err := FindCommit(repoPath, tc.revision)
		require.NoError(t, err)

		assert.Equal(t, tc.commit, foundCommit)
	}
}

func TestFindCommitFailures(t *testing.T) {
	_, repoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	revisions := []string{
		// Branch resolving to a commit with the same sha: https://github.com/src-d/go-git/issues/823
		"1942eed5cc108b19c7405106e81fa96125d0be22",
		// Anotated tags: https://github.com/src-d/go-git/issues/772
		"v1.0.0",
	}

	for _, rev := range revisions {
		_, err := FindCommit(repoPath, rev)

		assert.Errorf(t, err, "no ref found", "")
	}
}
