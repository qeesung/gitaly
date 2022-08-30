//go:build !gitaly_test_sha256

package ref

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func containsRef(refs [][]byte, ref string) bool {
	for _, b := range refs {
		if string(b) == ref {
			return true
		}
	}
	return false
}

func TestSuccessfulFindAllBranchNames(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repo, _, client := setupRefService(ctx, t)

	rpcRequest := &gitalypb.FindAllBranchNamesRequest{Repository: repo}
	c, err := client.FindAllBranchNames(ctx, rpcRequest)
	require.NoError(t, err)

	var names [][]byte
	for {
		r, err := c.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		names = append(names, r.GetNames()...)
	}

	expectedBranches := testhelper.MustReadFile(t, "testdata/branches.txt")
	for _, branch := range bytes.Split(bytes.TrimSpace(expectedBranches), []byte("\n")) {
		require.Contains(t, names, branch)
	}
}

func TestFindAllBranchNamesVeryLargeResponse(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, repoProto, _, client := setupRefService(ctx, t)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	updater, err := updateref.New(ctx, repo)
	require.NoError(t, err)

	// We want to create enough refs to overflow the default bufio.Scanner
	// buffer. Such an overflow will cause scanner.Bytes() to become invalid
	// at some point. That is expected behavior, but our tests did not
	// trigger it, so we got away with naively using scanner.Bytes() and
	// causing a bug: https://gitlab.com/gitlab-org/gitaly/issues/1473.
	refSizeLowerBound := 100
	numRefs := 2 * bufio.MaxScanTokenSize / refSizeLowerBound

	var testRefs []string
	for i := 0; i < numRefs; i++ {
		refName := fmt.Sprintf("refs/heads/test-%0100d", i)
		require.True(t, len(refName) > refSizeLowerBound, "ref %q must be larger than %d", refName, refSizeLowerBound)

		require.NoError(t, updater.Create(git.ReferenceName(refName), "HEAD"))
		testRefs = append(testRefs, refName)
	}

	require.NoError(t, updater.Commit())

	rpcRequest := &gitalypb.FindAllBranchNamesRequest{Repository: repoProto}

	c, err := client.FindAllBranchNames(ctx, rpcRequest)
	require.NoError(t, err)

	var names [][]byte
	for {
		r, err := c.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		names = append(names, r.GetNames()...)
	}

	for _, branch := range testRefs {
		require.Contains(t, names, []byte(branch), "branch missing from response: %q", branch)
	}
}

func TestEmptyFindAllBranchNamesRequest(t *testing.T) {
	_, client := setupRefServiceWithoutRepo(t)

	rpcRequest := &gitalypb.FindAllBranchNamesRequest{}
	ctx := testhelper.Context(t)
	c, err := client.FindAllBranchNames(ctx, rpcRequest)
	require.NoError(t, err)

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if helper.GrpcCode(recvError) != codes.InvalidArgument {
		t.Fatal(recvError)
	}
}

func TestInvalidRepoFindAllBranchNamesRequest(t *testing.T) {
	_, client := setupRefServiceWithoutRepo(t)

	repo := &gitalypb.Repository{StorageName: "default", RelativePath: "made/up/path"}
	rpcRequest := &gitalypb.FindAllBranchNamesRequest{Repository: repo}
	ctx := testhelper.Context(t)
	c, err := client.FindAllBranchNames(ctx, rpcRequest)
	require.NoError(t, err)

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if helper.GrpcCode(recvError) != codes.NotFound {
		t.Fatal(recvError)
	}
}

func TestSuccessfulFindAllTagNames(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repo, _, client := setupRefService(ctx, t)

	rpcRequest := &gitalypb.FindAllTagNamesRequest{Repository: repo}
	c, err := client.FindAllTagNames(ctx, rpcRequest)
	require.NoError(t, err)

	var names [][]byte
	for {
		r, err := c.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, r.GetNames()...)
	}

	for _, tag := range []string{"v1.0.0", "v1.1.0"} {
		if !containsRef(names, "refs/tags/"+tag) {
			t.Fatal("Expected to find tag", tag, "in all tag names")
		}
	}
}

func TestEmptyFindAllTagNamesRequest(t *testing.T) {
	_, client := setupRefServiceWithoutRepo(t)

	rpcRequest := &gitalypb.FindAllTagNamesRequest{}
	ctx := testhelper.Context(t)
	c, err := client.FindAllTagNames(ctx, rpcRequest)
	require.NoError(t, err)

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if helper.GrpcCode(recvError) != codes.InvalidArgument {
		t.Fatal(recvError)
	}
}

func TestInvalidRepoFindAllTagNamesRequest(t *testing.T) {
	_, client := setupRefServiceWithoutRepo(t)

	repo := &gitalypb.Repository{StorageName: "default", RelativePath: "made/up/path"}
	rpcRequest := &gitalypb.FindAllTagNamesRequest{Repository: repo}
	ctx := testhelper.Context(t)
	c, err := client.FindAllTagNames(ctx, rpcRequest)
	require.NoError(t, err)

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if helper.GrpcCode(recvError) != codes.NotFound {
		t.Fatal(recvError)
	}
}

func TestSuccessfulFindDefaultBranchName(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupRefService(ctx, t)
	rpcRequest := &gitalypb.FindDefaultBranchNameRequest{Repository: repo}

	// The testing repository has no main branch, so we create it and update
	// HEAD to it
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/main", "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863")
	gittest.Exec(t, cfg, "-C", repoPath, "symbolic-ref", "HEAD", "refs/heads/main")
	r, err := client.FindDefaultBranchName(ctx, rpcRequest)
	require.NoError(t, err)

	require.Equal(t, git.ReferenceName(r.GetName()), git.DefaultRef)
}

func TestSuccessfulFindDefaultBranchNameLegacy(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repo, _, client := setupRefService(ctx, t)
	rpcRequest := &gitalypb.FindDefaultBranchNameRequest{Repository: repo}
	r, err := client.FindDefaultBranchName(ctx, rpcRequest)
	require.NoError(t, err)

	require.Equal(t, git.ReferenceName(r.GetName()), git.LegacyDefaultRef)
}

func TestEmptyFindDefaultBranchNameRequest(t *testing.T) {
	_, client := setupRefServiceWithoutRepo(t)
	rpcRequest := &gitalypb.FindDefaultBranchNameRequest{}
	ctx := testhelper.Context(t)
	_, err := client.FindDefaultBranchName(ctx, rpcRequest)

	if helper.GrpcCode(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}

func TestInvalidRepoFindDefaultBranchNameRequest(t *testing.T) {
	cfg, client := setupRefServiceWithoutRepo(t)
	repo := &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: "/made/up/path"}
	rpcRequest := &gitalypb.FindDefaultBranchNameRequest{Repository: repo}
	ctx := testhelper.Context(t)
	_, err := client.FindDefaultBranchName(ctx, rpcRequest)

	if helper.GrpcCode(err) != codes.NotFound {
		t.Fatal(err)
	}
}

func TestSuccessfulFindLocalBranches(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.SimplifyFindLocalBranchesResponse).Run(t, testSuccessfulFindLocalBranches)
}

func testSuccessfulFindLocalBranches(t *testing.T, ctx context.Context) {
	_, repo, _, client := setupRefService(ctx, t)

	rpcRequest := &gitalypb.FindLocalBranchesRequest{Repository: repo}
	c, err := client.FindLocalBranches(ctx, rpcRequest)
	require.NoError(t, err)

	if featureflag.SimplifyFindLocalBranchesResponse.IsEnabled(ctx) {
		var branches []*gitalypb.Branch
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			branches = append(branches, r.GetLocalBranches()...)
		}

		for name, target := range localBranches {
			localBranch := &gitalypb.Branch{
				Name:         []byte(name),
				TargetCommit: target,
			}

			assertContainsBranch(t, branches, localBranch)
		}

	} else {
		var branches []*gitalypb.FindLocalBranchResponse
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			branches = append(branches, r.GetBranches()...)
		}

		for name, target := range localBranches {
			localBranch := &gitalypb.FindLocalBranchResponse{
				Name:          []byte(name),
				CommitId:      target.Id,
				CommitSubject: target.Subject,
				CommitAuthor: &gitalypb.FindLocalBranchCommitAuthor{
					Name:  target.Author.Name,
					Email: target.Author.Email,
					Date:  target.Author.Date,
				},
				CommitCommitter: &gitalypb.FindLocalBranchCommitAuthor{
					Name:  target.Committer.Name,
					Email: target.Committer.Email,
					Date:  target.Committer.Date,
				},
				Commit: target,
			}

			assertContainsLocalBranch(t, branches, localBranch)
		}
	}
}

func TestFindLocalBranchesHugeCommitter(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.SimplifyFindLocalBranchesResponse).Run(t, testFindLocalBranchesHugeCommitter)
}

func testFindLocalBranchesHugeCommitter(t *testing.T, ctx context.Context) {
	cfg, repo, repoPath, client := setupRefService(ctx, t)

	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("refs/heads/improve/awesome"),
		gittest.WithCommitterName(strings.Repeat("A", 100000)),
	)

	rpcRequest := &gitalypb.FindLocalBranchesRequest{Repository: repo}

	c, err := client.FindLocalBranches(ctx, rpcRequest)
	require.NoError(t, err)

	for {
		_, err := c.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
}

func TestFindLocalBranchesPagination(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.SimplifyFindLocalBranchesResponse).Run(t, testFindLocalBranchesPagination)
}

func testFindLocalBranchesPagination(t *testing.T, ctx context.Context) {
	_, repo, _, client := setupRefService(ctx, t)

	limit := 1
	rpcRequest := &gitalypb.FindLocalBranchesRequest{
		Repository: repo,
		PaginationParams: &gitalypb.PaginationParameter{
			Limit:     int32(limit),
			PageToken: "refs/heads/gitaly/squash-test",
		},
	}
	c, err := client.FindLocalBranches(ctx, rpcRequest)
	require.NoError(t, err)

	expectedBranch := "refs/heads/improve/awesome"
	target := localBranches[expectedBranch]

	if featureflag.SimplifyFindLocalBranchesResponse.IsEnabled(ctx) {
		var branches []*gitalypb.Branch
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			branches = append(branches, r.GetLocalBranches()...)
		}

		require.Len(t, branches, limit)

		branch := &gitalypb.Branch{
			Name:         []byte(expectedBranch),
			TargetCommit: target,
		}
		assertContainsBranch(t, branches, branch)
	} else {
		var branches []*gitalypb.FindLocalBranchResponse
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			branches = append(branches, r.GetBranches()...)
		}

		require.Len(t, branches, limit)

		branch := &gitalypb.FindLocalBranchResponse{
			Name:          []byte(expectedBranch),
			CommitId:      target.Id,
			CommitSubject: target.Subject,
			CommitAuthor: &gitalypb.FindLocalBranchCommitAuthor{
				Name:  target.Author.Name,
				Email: target.Author.Email,
				Date:  target.Author.Date,
			},
			CommitCommitter: &gitalypb.FindLocalBranchCommitAuthor{
				Name:  target.Committer.Name,
				Email: target.Committer.Email,
				Date:  target.Committer.Date,
			},
			Commit: target,
		}
		assertContainsLocalBranch(t, branches, branch)
	}
}

func TestFindLocalBranchesPaginationSequence(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.SimplifyFindLocalBranchesResponse).Run(t, testFindLocalBranchesPaginationSequence)
}

func testFindLocalBranchesPaginationSequence(t *testing.T, ctx context.Context) {
	_, repo, _, client := setupRefService(ctx, t)

	limit := 2
	firstRPCRequest := &gitalypb.FindLocalBranchesRequest{
		Repository: repo,
		PaginationParams: &gitalypb.PaginationParameter{
			Limit: int32(limit),
		},
	}
	c, err := client.FindLocalBranches(ctx, firstRPCRequest)
	require.NoError(t, err)

	if featureflag.SimplifyFindLocalBranchesResponse.IsEnabled(ctx) {
		var firstResponseBranches []*gitalypb.Branch
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			firstResponseBranches = append(firstResponseBranches, r.GetLocalBranches()...)
		}

		require.Len(t, firstResponseBranches, limit)

		secondRPCRequest := &gitalypb.FindLocalBranchesRequest{
			Repository: repo,
			PaginationParams: &gitalypb.PaginationParameter{
				Limit:     1,
				PageToken: string(firstResponseBranches[0].Name),
			},
		}
		c, err = client.FindLocalBranches(ctx, secondRPCRequest)
		require.NoError(t, err)

		var secondResponseBranches []*gitalypb.Branch
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			secondResponseBranches = append(secondResponseBranches, r.GetLocalBranches()...)
		}

		require.Len(t, secondResponseBranches, 1)
		require.Equal(t, firstResponseBranches[1], secondResponseBranches[0])
	} else {
		var firstResponseBranches []*gitalypb.FindLocalBranchResponse
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			firstResponseBranches = append(firstResponseBranches, r.GetBranches()...)
		}

		require.Len(t, firstResponseBranches, limit)

		secondRPCRequest := &gitalypb.FindLocalBranchesRequest{
			Repository: repo,
			PaginationParams: &gitalypb.PaginationParameter{
				Limit:     1,
				PageToken: string(firstResponseBranches[0].Name),
			},
		}
		c, err = client.FindLocalBranches(ctx, secondRPCRequest)
		require.NoError(t, err)

		var secondResponseBranches []*gitalypb.FindLocalBranchResponse
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			secondResponseBranches = append(secondResponseBranches, r.GetBranches()...)
		}

		require.Len(t, secondResponseBranches, 1)
		require.Equal(t, firstResponseBranches[1], secondResponseBranches[0])
	}
}

func TestFindLocalBranchesPaginationWithIncorrectToken(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.SimplifyFindLocalBranchesResponse).Run(t, testFindLocalBranchesPaginationWithIncorrectToken)
}

func testFindLocalBranchesPaginationWithIncorrectToken(t *testing.T, ctx context.Context) {
	_, repo, _, client := setupRefService(ctx, t)

	limit := 1
	rpcRequest := &gitalypb.FindLocalBranchesRequest{
		Repository: repo,
		PaginationParams: &gitalypb.PaginationParameter{
			Limit:     int32(limit),
			PageToken: "refs/heads/random-unknown-branch",
		},
	}
	c, err := client.FindLocalBranches(ctx, rpcRequest)
	require.NoError(t, err)

	_, err = c.Recv()
	require.NotEqual(t, err, io.EOF)
	testhelper.RequireGrpcError(t, helper.ErrInternalf("could not find page token"), err)
}

// Test that `s` contains the elements in `relativeOrder` in that order
// (relative to each other)
func isOrderedSubset(subset, set []string) bool {
	subsetIndex := 0 // The string we are currently looking for from `subset`
	for _, element := range set {
		if element != subset[subsetIndex] {
			continue
		}

		subsetIndex++

		if subsetIndex == len(subset) { // We found all elements in that order
			return true
		}
	}
	return false
}

func TestFindLocalBranchesSort(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.SimplifyFindLocalBranchesResponse).Run(t, testFindLocalBranchesSort)
}

func testFindLocalBranchesSort(t *testing.T, ctx context.Context) {
	testCases := []struct {
		desc          string
		relativeOrder []string
		sortBy        gitalypb.FindLocalBranchesRequest_SortBy
	}{
		{
			desc:          "In ascending order by name",
			relativeOrder: []string{"refs/heads/'test'", "refs/heads/100%branch", "refs/heads/improve/awesome", "refs/heads/master"},
			sortBy:        gitalypb.FindLocalBranchesRequest_NAME,
		},
		{
			desc:          "In ascending order by commiter date",
			relativeOrder: []string{"refs/heads/improve/awesome", "refs/heads/'test'", "refs/heads/100%branch", "refs/heads/master"},
			sortBy:        gitalypb.FindLocalBranchesRequest_UPDATED_ASC,
		},
		{
			desc:          "In descending order by commiter date",
			relativeOrder: []string{"refs/heads/master", "refs/heads/100%branch", "refs/heads/'test'", "refs/heads/improve/awesome"},
			sortBy:        gitalypb.FindLocalBranchesRequest_UPDATED_DESC,
		},
	}

	_, repo, _, client := setupRefService(ctx, t)

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			rpcRequest := &gitalypb.FindLocalBranchesRequest{Repository: repo, SortBy: testCase.sortBy}

			c, err := client.FindLocalBranches(ctx, rpcRequest)
			require.NoError(t, err)

			var branches []string
			for {
				r, err := c.Recv()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)

				if featureflag.SimplifyFindLocalBranchesResponse.IsEnabled(ctx) {
					for _, branch := range r.GetLocalBranches() {
						branches = append(branches, string(branch.Name))
					}
				} else {
					for _, branch := range r.GetBranches() {
						branches = append(branches, string(branch.Name))
					}
				}

			}

			if !isOrderedSubset(testCase.relativeOrder, branches) {
				t.Fatalf("%s: Expected branches to have relative order %v; got them as %v", testCase.desc, testCase.relativeOrder, branches)
			}
		})
	}
}

func TestEmptyFindLocalBranchesRequest(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.SimplifyFindLocalBranchesResponse).Run(t, testEmptyFindLocalBranchesRequest)
}

func testEmptyFindLocalBranchesRequest(t *testing.T, ctx context.Context) {
	_, client := setupRefServiceWithoutRepo(t)

	rpcRequest := &gitalypb.FindLocalBranchesRequest{}
	c, err := client.FindLocalBranches(ctx, rpcRequest)
	require.NoError(t, err)

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	testhelper.RequireGrpcError(t,
		helper.ErrInvalidArgumentf(gitalyOrPraefect(
			"GetStorageByName: no such storage: \"\"",
			"repo scoped: empty Repository",
		)),
		recvError,
	)
}

func TestSuccessfulFindAllBranchesRequest(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupRefService(ctx, t)

	remoteBranch := &gitalypb.FindAllBranchesResponse_Branch{
		Name: []byte("refs/remotes/origin/fake-remote-branch"),
		Target: &gitalypb.GitCommit{
			Id:        "913c66a37b4a45b9769037c55c2d238bd0942d2e",
			Subject:   []byte("Files, encoding and much more"),
			Body:      []byte("Files, encoding and much more\n\nSigned-off-by: Dmitriy Zaporozhets <dmitriy.zaporozhets@gmail.com>\n"),
			BodySize:  98,
			ParentIds: []string{"cfe32cf61b73a0d5e9f13e774abde7ff789b1660"},
			Author: &gitalypb.CommitAuthor{
				Name:     []byte("Dmitriy Zaporozhets"),
				Email:    []byte("dmitriy.zaporozhets@gmail.com"),
				Date:     &timestamppb.Timestamp{Seconds: 1393488896},
				Timezone: []byte("+0200"),
			},
			Committer: &gitalypb.CommitAuthor{
				Name:     []byte("Dmitriy Zaporozhets"),
				Email:    []byte("dmitriy.zaporozhets@gmail.com"),
				Date:     &timestamppb.Timestamp{Seconds: 1393488896},
				Timezone: []byte("+0200"),
			},
			SignatureType: gitalypb.SignatureType_PGP,
			TreeId:        "faafbe7fe23fb83c664c78aaded9566c8f934412",
		},
	}

	gittest.WriteRef(t, cfg, repoPath, "refs/remotes/origin/fake-remote-branch", git.ObjectID(remoteBranch.Target.Id))

	request := &gitalypb.FindAllBranchesRequest{Repository: repo}
	c, err := client.FindAllBranches(ctx, request)
	require.NoError(t, err)

	branches := readFindAllBranchesResponsesFromClient(t, c)

	// It contains local branches
	for name, target := range localBranches {
		branch := &gitalypb.FindAllBranchesResponse_Branch{
			Name:   []byte(name),
			Target: target,
		}
		assertContainsAllBranchesResponseBranch(t, branches, branch)
	}

	// It contains our fake remote branch
	assertContainsAllBranchesResponseBranch(t, branches, remoteBranch)
}

func TestSuccessfulFindAllBranchesRequestWithMergedBranches(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, repoProto, repoPath, client := setupRefService(ctx, t)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	localRefs := gittest.Exec(t, cfg, "-C", repoPath, "for-each-ref", "--format=%(refname:strip=2)", "refs/heads")
	for _, ref := range strings.Split(string(localRefs), "\n") {
		ref = strings.TrimSpace(ref)
		if _, ok := localBranches["refs/heads/"+ref]; ok || ref == "master" || ref == "" {
			continue
		}
		gittest.Exec(t, cfg, "-C", repoPath, "branch", "-D", ref)
	}

	expectedRefs := []string{"refs/heads/100%branch", "refs/heads/improve/awesome", "refs/heads/'test'"}

	var expectedBranches []*gitalypb.FindAllBranchesResponse_Branch
	for _, name := range expectedRefs {
		target, ok := localBranches[name]
		require.True(t, ok)

		branch := &gitalypb.FindAllBranchesResponse_Branch{
			Name:   []byte(name),
			Target: target,
		}
		expectedBranches = append(expectedBranches, branch)
	}

	masterCommit, err := repo.ReadCommit(ctx, "master")
	require.NoError(t, err)
	expectedBranches = append(expectedBranches, &gitalypb.FindAllBranchesResponse_Branch{
		Name:   []byte("refs/heads/master"),
		Target: masterCommit,
	})

	testCases := []struct {
		desc             string
		request          *gitalypb.FindAllBranchesRequest
		expectedBranches []*gitalypb.FindAllBranchesResponse_Branch
	}{
		{
			desc: "all merged branches",
			request: &gitalypb.FindAllBranchesRequest{
				Repository: repoProto,
				MergedOnly: true,
			},
			expectedBranches: expectedBranches,
		},
		{
			desc: "all merged from a list of branches",
			request: &gitalypb.FindAllBranchesRequest{
				Repository: repoProto,
				MergedOnly: true,
				MergedBranches: [][]byte{
					[]byte("refs/heads/100%branch"),
					[]byte("refs/heads/improve/awesome"),
					[]byte("refs/heads/gitaly-stuff"),
				},
			},
			expectedBranches: expectedBranches[:2],
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			c, err := client.FindAllBranches(ctx, testCase.request)
			require.NoError(t, err)

			branches := readFindAllBranchesResponsesFromClient(t, c)
			require.Len(t, branches, len(testCase.expectedBranches))

			for _, branch := range branches {
				// The GitCommit object returned by GetCommit() above and the one returned in the response
				// vary a lot. We can't guarantee that master will be fixed at a certain commit so we can't create
				// a structure for it manually, hence this hack.
				if string(branch.Name) == "refs/heads/master" {
					continue
				}

				assertContainsAllBranchesResponseBranch(t, testCase.expectedBranches, branch)
			}
		})
	}
}

func TestInvalidFindAllBranchesRequest(t *testing.T) {
	_, client := setupRefServiceWithoutRepo(t)

	for _, tc := range []struct {
		description string
		request     *gitalypb.FindAllBranchesRequest
		expectedErr error
	}{
		{
			description: "Empty request",
			request:     &gitalypb.FindAllBranchesRequest{},
			expectedErr: helper.ErrInvalidArgumentf(gitalyOrPraefect(
				"GetStorageByName: no such storage: \"\"",
				"repo scoped: empty Repository",
			)),
		},
		{
			description: "Invalid repo",
			request: &gitalypb.FindAllBranchesRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "fake",
					RelativePath: "repo",
				},
			},
			expectedErr: helper.ErrInvalidArgumentf(gitalyOrPraefect(
				"GetStorageByName: no such storage: \"fake\"",
				"repo scoped: invalid Repository",
			)),
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			ctx := testhelper.Context(t)
			c, err := client.FindAllBranches(ctx, tc.request)
			require.NoError(t, err)

			var recvError error
			for recvError == nil {
				_, recvError = c.Recv()
			}

			testhelper.RequireGrpcError(t, tc.expectedErr, recvError)
		})
	}
}

func readFindAllBranchesResponsesFromClient(t *testing.T, c gitalypb.RefService_FindAllBranchesClient) (branches []*gitalypb.FindAllBranchesResponse_Branch) {
	for {
		r, err := c.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		branches = append(branches, r.GetBranches()...)
	}

	return
}

func TestListTagNamesContainingCommit(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repoProto, _, client := setupRefService(ctx, t)

	testCases := []struct {
		description string
		commitID    string
		code        codes.Code
		limit       uint32
		tags        []string
	}{
		{
			description: "no commit ID",
			commitID:    "",
			code:        codes.InvalidArgument,
		},
		{
			description: "current master HEAD",
			commitID:    "e63f41fe459e62e1228fcef60d7189127aeba95a",
			code:        codes.OK,
			tags:        []string{},
		},
		{
			description: "init commit",
			commitID:    "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			code:        codes.OK,
			tags:        []string{"v1.0.0", "v1.1.0"},
		},
		{
			description: "limited response size",
			commitID:    "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			code:        codes.OK,
			limit:       1,
			tags:        []string{"v1.0.0"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			request := &gitalypb.ListTagNamesContainingCommitRequest{Repository: repoProto, CommitId: tc.commitID}

			c, err := client.ListTagNamesContainingCommit(ctx, request)
			require.NoError(t, err)

			var names []string
			for {
				r, err := c.Recv()
				if err == io.EOF {
					break
				} else if tc.code != codes.OK {
					testhelper.RequireGrpcCode(t, err, tc.code)

					return
				}
				require.NoError(t, err)

				for _, name := range r.GetTagNames() {
					names = append(names, string(name))
				}
			}

			// Test for inclusion instead of equality because new refs
			// will get added to the gitlab-test repo over time.
			require.Subset(t, names, tc.tags)
		})
	}
}

func TestListBranchNamesContainingCommit(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repo, _, client := setupRefService(ctx, t)

	testCases := []struct {
		description string
		commitID    string
		code        codes.Code
		limit       uint32
		branches    []string
	}{
		{
			description: "no commit ID",
			commitID:    "",
			code:        codes.InvalidArgument,
		},
		{
			description: "current master HEAD",
			commitID:    "e63f41fe459e62e1228fcef60d7189127aeba95a",
			code:        codes.OK,
			branches:    []string{"master"},
		},
		{
			// gitlab-test contains a branch refs/heads/1942eed5cc108b19c7405106e81fa96125d0be22
			// which is in conflict with a commit with the same ID
			description: "branch name is also commit id",
			commitID:    "1942eed5cc108b19c7405106e81fa96125d0be22",
			code:        codes.OK,
			branches:    []string{"1942eed5cc108b19c7405106e81fa96125d0be22"},
		},
		{
			description: "init commit",
			commitID:    "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			code:        codes.OK,
			branches: []string{
				"deleted-image-test",
				"ends-with.json",
				"master",
				"conflict-non-utf8",
				"'test'",
				"ʕ•ᴥ•ʔ",
				"'test'",
				"100%branch",
			},
		},
		{
			description: "init commit",
			commitID:    "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			code:        codes.OK,
			limit:       1,
			branches:    []string{"'test'"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			request := &gitalypb.ListBranchNamesContainingCommitRequest{Repository: repo, CommitId: tc.commitID}

			c, err := client.ListBranchNamesContainingCommit(ctx, request)
			require.NoError(t, err)

			var names []string
			for {
				r, err := c.Recv()
				if err == io.EOF {
					break
				} else if tc.code != codes.OK {
					testhelper.RequireGrpcCode(t, err, tc.code)

					return
				}
				require.NoError(t, err)

				for _, name := range r.GetBranchNames() {
					names = append(names, string(name))
				}
			}

			// Test for inclusion instead of equality because new refs
			// will get added to the gitlab-test repo over time.
			require.Subset(t, names, tc.branches)
		})
	}
}
