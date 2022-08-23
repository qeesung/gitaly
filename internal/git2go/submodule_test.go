//go:build static && system_libgit2 && !gitaly_test_sha256

package git2go

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/lstree"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestSubmodule(t *testing.T) {
	commitMessage := []byte("Update Submodule message")

	testCases := []struct {
		desc           string
		command        SubmoduleCommand
		expectedStderr string
	}{
		{
			desc: "Update submodule",
			command: SubmoduleCommand{
				AuthorName: string(gittest.TestUser.Name),
				AuthorMail: string(gittest.TestUser.Email),
				Message:    string(commitMessage),
				CommitSHA:  "41fa1bc9e0f0630ced6a8a211d60c2af425ecc2d",
				Submodule:  "gitlab-grack",
				Branch:     "master",
			},
		},
		{
			desc: "Update submodule inside folder",
			command: SubmoduleCommand{
				AuthorName: string(gittest.TestUser.Name),
				AuthorMail: string(gittest.TestUser.Email),
				Message:    string(commitMessage),
				CommitSHA:  "e25eda1fece24ac7a03624ed1320f82396f35bd8",
				Submodule:  "test_inside_folder/another_folder/six",
				Branch:     "submodule_inside_folder",
			},
		},
		{
			desc: "Invalid branch",
			command: SubmoduleCommand{
				AuthorName: string(gittest.TestUser.Name),
				AuthorMail: string(gittest.TestUser.Email),
				Message:    string(commitMessage),
				CommitSHA:  "e25eda1fece24ac7a03624ed1320f82396f35bd8",
				Submodule:  "test_inside_folder/another_folder/six",
				Branch:     "non/existent",
			},
			expectedStderr: "Invalid branch",
		},
		{
			desc: "Invalid submodule",
			command: SubmoduleCommand{
				AuthorName: string(gittest.TestUser.Name),
				AuthorMail: string(gittest.TestUser.Email),
				Message:    string(commitMessage),
				CommitSHA:  "e25eda1fece24ac7a03624ed1320f82396f35bd8",
				Submodule:  "non-existent-submodule",
				Branch:     "master",
			},
			expectedStderr: "Invalid submodule path",
		},
		{
			desc: "Duplicate reference",
			command: SubmoduleCommand{
				AuthorName: string(gittest.TestUser.Name),
				AuthorMail: string(gittest.TestUser.Email),
				Message:    string(commitMessage),
				CommitSHA:  "409f37c4f05865e4fb208c771485f211a22c4c2d",
				Submodule:  "six",
				Branch:     "master",
			},
			expectedStderr: "The submodule six is already at 409f37c4f",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, repoProto, repoPath := testcfg.BuildWithRepo(t)
			testcfg.BuildGitalyGit2Go(t, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)
			executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

			tc.command.Repository = repoPath
			ctx := testhelper.Context(t)

			response, err := executor.Submodule(ctx, repo, tc.command)
			if tc.expectedStderr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedStderr)
				return
			}
			require.NoError(t, err)

			commit, err := repo.ReadCommit(ctx, git.Revision(response.CommitID))
			require.NoError(t, err)
			require.Equal(t, commit.Author.Email, gittest.TestUser.Email)
			require.Equal(t, commit.Committer.Email, gittest.TestUser.Email)
			require.Equal(t, commit.Subject, commitMessage)

			entry := gittest.Exec(
				t,
				cfg,
				"-C",
				repoPath,
				"ls-tree",
				"-z",
				fmt.Sprintf("%s^{tree}:", response.CommitID),
				tc.command.Submodule,
			)
			parser := lstree.NewParser(bytes.NewReader(entry), git.ObjectHashSHA1)
			parsedEntry, err := parser.NextEntry()
			require.NoError(t, err)
			require.Equal(t, tc.command.Submodule, parsedEntry.Path)
			require.Equal(t, tc.command.CommitSHA, parsedEntry.ObjectID.String())
		})
	}
}
