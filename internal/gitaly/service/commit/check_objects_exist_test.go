package commit

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestCheckObjectsExist(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupCommitServiceWithRepo(ctx, t, true)

	// write a few commitIDs we can use
	commitID1 := gittest.WriteCommit(t, cfg, repoPath)
	commitID2 := gittest.WriteCommit(t, cfg, repoPath)
	commitID3 := gittest.WriteCommit(t, cfg, repoPath)

	// remove a ref from the repository so we know it doesn't exist
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "-d", "refs/heads/many_files")

	nonexistingObject := "abcdefg"
	cmd := gittest.NewCommand(t, cfg, "-C", repoPath, "rev-parse", nonexistingObject)
	require.Error(t, cmd.Wait(), "ensure the object doesn't exist")

	testCases := []struct {
		desc               string
		input              [][]byte
		revisionsExistence map[string]bool
		returnCode         codes.Code
	}{
		{
			desc: "commit ids and refs that exist",
			input: [][]byte{
				[]byte(commitID1),
				[]byte("master"),
				[]byte(commitID2),
				[]byte(commitID3),
				[]byte("feature"),
			},
			revisionsExistence: map[string]bool{
				"master":           true,
				commitID2.String(): true,
				commitID3.String(): true,
				"feature":          true,
			},
			returnCode: codes.OK,
		},
		{
			desc: "ref and objects missing",
			input: [][]byte{
				[]byte(commitID1),
				[]byte("master"),
				[]byte(commitID2),
				[]byte(commitID3),
				[]byte("feature"),
				[]byte("many_files"),
				[]byte(nonexistingObject),
			},
			revisionsExistence: map[string]bool{
				"master":           true,
				commitID2.String(): true,
				commitID3.String(): true,
				"feature":          true,
				"many_files":       false,
				nonexistingObject:  false,
			},
			returnCode: codes.OK,
		},
		{
			desc:               "empty input",
			input:              [][]byte{},
			returnCode:         codes.OK,
			revisionsExistence: map[string]bool{},
		},
		{
			desc:       "invalid input",
			input:      [][]byte{[]byte("-not-a-rev")},
			returnCode: codes.InvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			c, err := client.CheckObjectsExist(ctx)
			require.NoError(t, err)

			require.NoError(t, c.Send(
				&gitalypb.CheckObjectsExistRequest{
					Repository: repo,
					Revisions:  tc.input,
				},
			))
			require.NoError(t, c.CloseSend())

			for {
				resp, err := c.Recv()
				if tc.returnCode != codes.OK {
					testhelper.RequireGrpcCode(t, err, tc.returnCode)
					break
				} else if err != nil {
					require.Error(t, err, io.EOF)
					break
				}

				actualRevisionsExistence := make(map[string]bool)
				for _, revisionExistence := range resp.GetRevisions() {
					actualRevisionsExistence[string(revisionExistence.GetName())] = revisionExistence.GetExists()
				}
				assert.Equal(t, tc.revisionsExistence, actualRevisionsExistence)
			}
		})
	}
}
