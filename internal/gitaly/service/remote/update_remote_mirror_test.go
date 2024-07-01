package remote

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	repositorysvc "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type commandFactoryWrapper struct {
	git.CommandFactory
	newFunc func(context.Context, storage.Repository, git.Command, ...git.CmdOpt) (*command.Command, error)
}

func (w commandFactoryWrapper) New(ctx context.Context, repo storage.Repository, sc git.Command, opts ...git.CmdOpt) (*command.Command, error) {
	return w.newFunc(ctx, repo, sc, opts...)
}

func TestUpdateRemoteMirror(t *testing.T) {
	t.Parallel()

	type refs map[string][]string

	type expectedError struct {
		contains string
		code     codes.Code
	}

	for _, tc := range []struct {
		desc                 string
		sourceRefs           refs
		sourceSymRefs        map[string]string
		mirrorRefs           refs
		mirrorSymRefs        map[string]string
		keepDivergentRefs    bool
		onlyBranchesMatching []string
		wrapCommandFactory   func(testing.TB, git.CommandFactory) git.CommandFactory
		requests             []*gitalypb.UpdateRemoteMirrorRequest
		expectedError        *expectedError
		response             *gitalypb.UpdateRemoteMirrorResponse
		expectedMirrorRefs   map[string]string
	}{
		{
			desc: "empty mirror source works",
			mirrorRefs: refs{
				"refs/heads/tags": {"commit 1"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				"refs/heads/tags": "commit 1",
			},
		},
		{
			desc:     "mirror is up to date",
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
				"refs/tags/tag":        {"commit 1"},
			},
			mirrorRefs: refs{
				string(git.DefaultRef): {"commit 1"},
				"refs/tags/tag":        {"commit 1"},
			},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef): "commit 1",
				"refs/tags/tag":        "commit 1",
			},
		},
		{
			desc: "creates missing references",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
				"refs/tags/tag":        {"commit 1"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef): "commit 1",
				"refs/tags/tag":        "commit 1",
			},
		},
		{
			desc: "updates outdated references",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1", "commit 2"},
				"refs/tags/tag":        {"commit 1", "commit 2"},
			},
			mirrorRefs: refs{
				string(git.DefaultRef): {"commit 1"},
				"refs/tags/tag":        {"commit 1"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef): "commit 2",
				"refs/tags/tag":        "commit 2",
			},
		},
		{
			desc: "deletes unneeded references",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
			},
			mirrorRefs: refs{
				string(git.DefaultRef): {"commit 1"},
				"refs/heads/branch":    {"commit 1"},
				"refs/tags/tag":        {"commit 1"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef): "commit 1",
			},
		},
		{
			desc: "keeps extra branches in remote not merged in local default branch",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
			},
			mirrorRefs: refs{
				string(git.DefaultRef):  {"commit 1"},
				"refs/heads/merged":     {"commit 1"},
				"refs/heads/not-merged": {"commit 1", "commit 2"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef):  "commit 1",
				"refs/heads/not-merged": "commit 2",
			},
		},
		{
			desc: "updates branches that match the branch selector",
			sourceRefs: refs{
				string(git.DefaultRef):   {"commit 1"},
				"refs/heads/matched":     {"commit 1"},
				"refs/heads/not-matched": {"commit 1"},
			},
			mirrorRefs: refs{
				"refs/tags/tag": {"commit 1"},
			},
			onlyBranchesMatching: []string{"matched"},
			response:             &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				"refs/heads/matched": "commit 1",
			},
		},
		{
			desc: "updates branches that match the branch selector with wildcard",
			sourceRefs: refs{
				string(git.DefaultRef):   {"commit 1"},
				"refs/heads/matched":     {"commit 1"},
				"refs/heads/not-matched": {"commit 1"},
			},
			mirrorRefs: refs{
				"refs/tags/tag": {"commit 1"},
			},
			onlyBranchesMatching: []string{"*matched"},
			response:             &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				"refs/heads/not-matched": "commit 1",
				"refs/heads/matched":     "commit 1",
			},
		},
		{
			desc: "deletes unneeded references that match the branch selector",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
			},
			mirrorRefs: refs{
				string(git.DefaultRef):   {"commit 1"},
				"refs/heads/matched":     {"commit 1"},
				"refs/heads/not-matched": {"commit 1"},
				"refs/tags/tag":          {"commit 1"},
			},
			onlyBranchesMatching: []string{"matched"},
			response:             &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef):   "commit 1",
				"refs/heads/not-matched": "commit 1",
			},
		},
		{
			desc: "ignores diverged branches not matched by the branch selector",
			sourceRefs: refs{
				"refs/heads/matched":  {"commit 1"},
				"refs/heads/diverged": {"commit 1"},
			},
			onlyBranchesMatching: []string{"matched"},
			keepDivergentRefs:    true,
			mirrorRefs: refs{
				"refs/heads/matched":  {"commit 1"},
				"refs/heads/diverged": {"commit 2"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				"refs/heads/matched":  "commit 1",
				"refs/heads/diverged": "commit 2",
			},
		},
		{
			desc: "does not delete refs with KeepDivergentRefs",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
			},
			keepDivergentRefs: true,
			mirrorRefs: refs{
				string(git.DefaultRef): {"commit 1"},
				"refs/heads/branch":    {"commit 1"},
				"refs/tags/tag":        {"commit 1"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef): "commit 1",
				"refs/heads/branch":    "commit 1",
				"refs/tags/tag":        "commit 1",
			},
		},
		{
			desc: "updating branch called tag works",
			sourceRefs: refs{
				"refs/heads/tag": {"commit 1", "commit 2"},
			},
			mirrorRefs: refs{
				"refs/heads/tag": {"commit 1"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				"refs/heads/tag": "commit 2",
			},
		},
		{
			desc: "works if tag and branch named the same",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
				"refs/tags/master":     {"commit 1"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef): "commit 1",
				"refs/tags/master":     "commit 1",
			},
		},
		{
			desc: "only local branches are considered",
			sourceRefs: refs{
				string(git.DefaultRef):            {"commit 1"},
				"refs/remote/local-remote/branch": {"commit 1"},
			},
			mirrorRefs: refs{
				"refs/remote/mirror-remote/branch": {"commit 1"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef):             "commit 1",
				"refs/remote/mirror-remote/branch": "commit 1",
			},
		},
		{
			desc: "creates branches matching selector",
			sourceRefs: refs{
				"refs/heads/matches":        {"commit 1"},
				"refs/heads/does-not-match": {"commit 2"},
				"refs/tags/tag":             {"commit 3"},
			},
			onlyBranchesMatching: []string{"matches"},
			response:             &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				"refs/heads/matches": "commit 1",
				"refs/tags/tag":      "commit 3",
			},
		},
		{
			desc: "updates branches matching selector",
			sourceRefs: refs{
				"refs/heads/matches":        {"commit 1", "commit 2"},
				"refs/heads/does-not-match": {"commit 3", "commit 4"},
				"refs/tags/tag":             {"commit 6"},
			},
			mirrorRefs: refs{
				"refs/heads/matches":        {"commit 1"},
				"refs/heads/does-not-match": {"commit 3"},
				"refs/tags/tag":             {"commit 5"},
			},
			onlyBranchesMatching: []string{"matches"},
			response:             &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				"refs/heads/matches":        "commit 2",
				"refs/heads/does-not-match": "commit 3",
				"refs/tags/tag":             "commit 6",
			},
		},
		{
			// https://gitlab.com/gitlab-org/gitaly/-/issues/3509
			desc: "overwrites diverged references without KeepDivergentRefs",
			sourceRefs: refs{
				"refs/heads/non-diverged": {"commit 1", "commit 2"},
				string(git.DefaultRef):    {"commit 3"},
				"refs/tags/tag-1":         {"commit 1"},
			},
			mirrorRefs: refs{
				"refs/heads/non-diverged": {"commit 1"},
				string(git.DefaultRef):    {"commit 3", "ahead"},
				"refs/tags/tag-1":         {"commit 2"},
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				"refs/heads/non-diverged": "commit 2",
				string(git.DefaultRef):    "commit 3",
				"refs/tags/tag-1":         "commit 1",
			},
		},
		{
			// https://gitlab.com/gitlab-org/gitaly/-/issues/3509
			desc: "keeps diverged references with KeepDivergentRefs",
			sourceRefs: refs{
				"refs/heads/non-diverged": {"commit 1", "commit 2"},
				string(git.DefaultRef):    {"commit 3"},
				"refs/tags/tag-1":         {"commit 1"},
			},
			mirrorRefs: refs{
				"refs/heads/non-diverged": {"commit 1"},
				string(git.DefaultRef):    {"commit 3", "ahead"},
				"refs/tags/tag-1":         {"commit 2"},
			},
			keepDivergentRefs: true,
			response: &gitalypb.UpdateRemoteMirrorResponse{
				DivergentRefs: [][]byte{
					[]byte(string(git.DefaultRef)),
					[]byte("refs/tags/tag-1"),
				},
			},
			expectedMirrorRefs: map[string]string{
				"refs/heads/non-diverged": "commit 2",
				string(git.DefaultRef):    "ahead",
				"refs/tags/tag-1":         "commit 2",
			},
		},
		{
			desc: "doesn't force push over refs that diverged after they were checked with KeepDivergentRefs",
			sourceRefs: refs{
				"refs/heads/diverging":     {"commit 1", "commit 2"},
				"refs/heads/non-diverging": {"commit-3"},
			},
			mirrorRefs: refs{
				"refs/heads/diverging":     {"commit 1"},
				"refs/heads/non-diverging": {"commit-3"},
			},
			keepDivergentRefs: true,
			wrapCommandFactory: func(tb testing.TB, original git.CommandFactory) git.CommandFactory {
				return commandFactoryWrapper{
					CommandFactory: original,
					newFunc: func(ctx context.Context, repo storage.Repository, sc git.Command, opts ...git.CmdOpt) (*command.Command, error) {
						if sc.Name == "push" {
							// This is really hacky: we extract the
							// remote name from the subcommands
							// arguments. But honestly, the whole way of
							// how we hijack the command factory is kind
							// of hacky in the first place.
							remoteName := sc.Args[0]
							require.Contains(tb, remoteName, "inmemory-")

							// Make the branch diverge on the remote before actually performing the pushes the RPC
							// is attempting to perform to simulate a ref diverging after the RPC has performed
							// its checks.
							cmd, err := original.New(ctx, repo, git.Command{
								Name:  "push",
								Flags: []git.Option{git.Flag{Name: "--force"}},
								Args:  []string{remoteName, "refs/heads/non-diverging:refs/heads/diverging"},
							}, opts...)
							if !assert.NoError(tb, err) {
								return nil, err
							}
							assert.NoError(tb, cmd.Wait())
						}

						return original.New(ctx, repo, sc, opts...)
					},
				}
			},
			expectedError: &expectedError{
				contains: "Updates were rejected because a pushed branch tip is behind its remote",
				code:     codes.Internal,
			},
		},
		{
			desc: "ignores symbolic references in source repo",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
			},
			sourceSymRefs: map[string]string{
				"refs/heads/symbolic-reference": string(git.DefaultRef),
			},
			onlyBranchesMatching: []string{"symbolic-reference"},
			response:             &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs:   map[string]string{},
		},
		{
			desc: "ignores symbolic refs on the mirror",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
			},
			mirrorRefs: refs{
				string(git.DefaultRef): {"commit 1"},
			},
			mirrorSymRefs: map[string]string{
				"refs/heads/symbolic-reference": string(git.DefaultRef),
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				// If the symbolic reference was not ignored, master would get deleted
				// as it's the branch pointed to by a symbolic ref not present in the source
				// repo.
				string(git.DefaultRef):          "commit 1",
				"refs/heads/symbolic-reference": "commit 1",
			},
		},
		{
			desc: "ignores symbolic refs and pushes the branch successfully",
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
			},
			sourceSymRefs: map[string]string{
				"refs/heads/symbolic-reference": string(git.DefaultRef),
			},
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef): "commit 1",
			},
		},
		{
			desc: "push batching works",
			sourceRefs: func() refs {
				out := refs{}
				for i := 0; i < 2*pushBatchSize+1; i++ {
					out[fmt.Sprintf("refs/heads/branch-%d", i)] = []string{"commit 1"}
				}
				return out
			}(),
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: func() map[string]string {
				out := map[string]string{}
				for i := 0; i < 2*pushBatchSize+1; i++ {
					out[fmt.Sprintf("refs/heads/branch-%d", i)] = "commit 1"
				}
				return out
			}(),
		},
		{
			desc: "pushes default branch in the first batch",
			wrapCommandFactory: func(tb testing.TB, original git.CommandFactory) git.CommandFactory {
				firstPush := true
				return commandFactoryWrapper{
					CommandFactory: original,
					newFunc: func(ctx context.Context, repo storage.Repository, sc git.Command, opts ...git.CmdOpt) (*command.Command, error) {
						if sc.Name == "push" && firstPush {
							firstPush = false
							args, err := sc.CommandArgs()
							assert.NoError(tb, err)
							assert.Contains(tb, args, string(git.DefaultRef), "first push should contain the default branch")
						}

						return original.New(ctx, repo, sc, opts...)
					},
				}
			},
			sourceRefs: func() refs {
				out := refs{string(git.DefaultRef): []string{"commit 1"}}
				for i := 0; i < 2*pushBatchSize; i++ {
					out[fmt.Sprintf("refs/heads/branch-%d", i)] = []string{"commit 1"}
				}
				return out
			}(),
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			expectedMirrorRefs: func() map[string]string {
				out := map[string]string{string(git.DefaultRef): "commit 1"}
				for i := 0; i < 2*pushBatchSize; i++ {
					out[fmt.Sprintf("refs/heads/branch-%d", i)] = "commit 1"
				}
				return out
			}(),
		},
		{
			desc: "limits the number of divergent refs returned",
			sourceRefs: func() refs {
				out := refs{}
				for i := 0; i < maxDivergentRefs+1; i++ {
					out[fmt.Sprintf("refs/heads/branch-%03d", i)] = []string{"commit 1"}
				}
				return out
			}(),
			mirrorRefs: func() refs {
				out := refs{}
				for i := 0; i < maxDivergentRefs+1; i++ {
					out[fmt.Sprintf("refs/heads/branch-%03d", i)] = []string{"commit 2"}
				}
				return out
			}(),
			keepDivergentRefs: true,
			response: &gitalypb.UpdateRemoteMirrorResponse{
				DivergentRefs: func() [][]byte {
					out := make([][]byte, maxDivergentRefs)
					for i := range out {
						out[i] = []byte(fmt.Sprintf("refs/heads/branch-%03d", i))
					}
					return out
				}(),
			},
			expectedMirrorRefs: func() map[string]string {
				out := map[string]string{}
				for i := 0; i < maxDivergentRefs+1; i++ {
					out[fmt.Sprintf("refs/heads/branch-%03d", i)] = "commit 2"
				}
				return out
			}(),
		},
		{
			desc:     "no F/D conflicts when the ref is an ancestor",
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			sourceRefs: refs{
				string(git.DefaultRef): {"commit 1"},
				"refs/heads/branch":    {"commit 1", "commit 2"},
			},
			mirrorRefs: refs{
				"refs/heads/branch/conflict": {"commit 1"},
			},
			expectedMirrorRefs: map[string]string{
				string(git.DefaultRef): "commit 1",
				"refs/heads/branch":    "commit 2",
			},
		},
		{
			desc:     "F/D conflicts when the ref is not an ancestor",
			response: &gitalypb.UpdateRemoteMirrorResponse{},
			sourceRefs: refs{
				"refs/heads/branch": {"commit 1"},
			},
			mirrorRefs: refs{
				"refs/heads/branch/conflict": {"commit 2"},
			},
			expectedError: &expectedError{
				contains: "push to mirror: git push: exit status 1",
				code:     codes.Internal,
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t)

			addr := testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
				gitalypb.RegisterRepositoryServiceServer(srv, repositorysvc.NewServer(deps))

				cmdFactory := deps.GetGitCmdFactory()
				if tc.wrapCommandFactory != nil {
					cmdFactory = tc.wrapCommandFactory(t, deps.GetGitCmdFactory())
				}
				deps.GitCmdFactory = cmdFactory

				gitalypb.RegisterRemoteServiceServer(srv, NewServer(deps))
			})
			cfg.SocketPath = addr

			mirrorRepoPb, mirrorRepoPath := gittest.CreateRepository(t, ctx, cfg)
			mirrorRepo := localrepo.NewTestRepo(t, cfg, mirrorRepoPb)
			sourceRepoPb, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
			sourceRepo := localrepo.NewTestRepo(t, cfg, sourceRepoPb)

			// construct the starting state of the repositories
			for _, c := range []struct {
				repo       *localrepo.Repo
				references refs
			}{
				{
					repo:       sourceRepo,
					references: tc.sourceRefs,
				},
				{
					repo:       mirrorRepo,
					references: tc.mirrorRefs,
				},
			} {
				repoPath, err := c.repo.Path(ctx)
				require.NoError(t, err)

				// We compute the commits in a separate step as many of the tests
				// use a small handful of commits and then update many references to
				// point to that small set. So by precomputing commits we save a few
				// hundred invocations of git-hash-object(1), git-write-tree(1) and
				// git-commit-tree(1) each.
				commitsByMessage := map[string]git.ObjectID{}
				for _, commits := range c.references {
					var commitOID git.ObjectID
					for i, commit := range commits {
						if existingOID, ok := commitsByMessage[commit]; ok {
							commitOID = existingOID
							continue
						}

						var parents []git.ObjectID
						if i != 0 && commitOID != "" {
							parents = []git.ObjectID{commitOID}
						}

						commitOID = gittest.WriteCommit(t, cfg, repoPath,
							gittest.WithMessage(commit),
							gittest.WithParents(parents...),
						)

						commitsByMessage[commit] = commitOID
					}
				}

				// And then finally we update the references via a single call to
				// the updateref package instead of spawning hundreds of them.
				updater, err := updateref.New(ctx, c.repo, updateref.WithDisabledTransactions())
				require.NoError(t, err)
				defer testhelper.MustClose(t, updater)

				require.NoError(t, updater.Start())
				for reference, commits := range c.references {
					// Set the reference to the last commit in the list
					require.NoError(t, updater.Create(git.ReferenceName(reference), commitsByMessage[commits[len(commits)-1]]))
				}
				require.NoError(t, updater.Commit())
			}

			for repoPath, symRefs := range map[string]map[string]string{
				sourceRepoPath: tc.sourceSymRefs,
				mirrorRepoPath: tc.mirrorSymRefs,
			} {
				for symRef, targetRef := range symRefs {
					gittest.Exec(t, cfg, "-C", repoPath, "symbolic-ref", symRef, targetRef)
				}
			}

			client, conn := newRemoteClient(t, addr)
			defer conn.Close()

			stream, err := client.UpdateRemoteMirror(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(&gitalypb.UpdateRemoteMirrorRequest{
				Repository: sourceRepoPb,
				Remote: &gitalypb.UpdateRemoteMirrorRequest_Remote{
					Url: mirrorRepoPath,
				},
				KeepDivergentRefs: tc.keepDivergentRefs,
			}))

			for _, pattern := range tc.onlyBranchesMatching {
				require.NoError(t, stream.Send(&gitalypb.UpdateRemoteMirrorRequest{
					OnlyBranchesMatching: [][]byte{[]byte(pattern)},
				}))
			}

			resp, err := stream.CloseAndRecv()
			if tc.expectedError != nil {
				testhelper.RequireGrpcCode(t, err, tc.expectedError.code)
				require.Contains(t, err.Error(), tc.expectedError.contains)
				return
			}

			require.NoError(t, err)
			testhelper.ProtoEqual(t, tc.response, resp)

			// Check that the refs on the mirror now refer to the correct commits.
			// This is done by checking the commit messages as the commits are otherwise
			// the same.
			actualMirrorRefs := map[string]string{}

			refLines := strings.Split(text.ChompBytes(gittest.Exec(t, cfg, "-C", mirrorRepoPath, "for-each-ref", "--format=%(refname)%00%(contents:subject)")), "\n")
			for _, line := range refLines {
				if line == "" {
					continue
				}

				split := strings.Split(line, "\000")
				actualMirrorRefs[split[0]] = split[1]
			}

			require.Equal(t, tc.expectedMirrorRefs, actualMirrorRefs)
		})
	}
}

func TestUpdateRemoteMirror_Validations(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRemoteService(t, ctx)

	testCases := []struct {
		expectedErr error
		setup       func(t *testing.T) *gitalypb.UpdateRemoteMirrorRequest
		desc        string
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) *gitalypb.UpdateRemoteMirrorRequest {
				return &gitalypb.UpdateRemoteMirrorRequest{
					Repository: nil,
					Remote: &gitalypb.UpdateRemoteMirrorRequest_Remote{
						Url: "something",
					},
				}
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "no Remote",
			setup: func(t *testing.T) *gitalypb.UpdateRemoteMirrorRequest {
				mirrorRepoPb, _ := gittest.CreateRepository(t, ctx, cfg)

				return &gitalypb.UpdateRemoteMirrorRequest{
					Repository: mirrorRepoPb,
				}
			},
			expectedErr: structerr.NewInvalidArgument("missing Remote"),
		},

		{
			desc: "remote is missing URL",
			setup: func(t *testing.T) *gitalypb.UpdateRemoteMirrorRequest {
				mirrorRepoPb, _ := gittest.CreateRepository(t, ctx, cfg)

				return &gitalypb.UpdateRemoteMirrorRequest{
					Repository: mirrorRepoPb,
					Remote: &gitalypb.UpdateRemoteMirrorRequest_Remote{
						Url: "",
					},
				}
			},
			expectedErr: structerr.NewInvalidArgument("remote is missing URL"),
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			stream, err := client.UpdateRemoteMirror(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(tc.setup(t)))

			_, err = stream.CloseAndRecv()
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
