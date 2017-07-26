package ref

import (
	"bytes"
	"io"
	"io/ioutil"
	"testing"

	"github.com/golang/protobuf/ptypes/timestamp"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	rpcRequest := &pb.FindAllBranchNamesRequest{Repository: testRepo}

	c, err := client.FindAllBranchNames(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	var names [][]byte
	for {
		r, err := c.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, r.GetNames()...)
	}
	for _, branch := range []string{"master", "100%branch", "improve/awesome", "'test'"} {
		if !containsRef(names, "refs/heads/"+branch) {
			t.Fatalf("Expected to find branch %q in all branch names", branch)
		}
	}
}

func TestEmptyFindAllBranchNamesRequest(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	rpcRequest := &pb.FindAllBranchNamesRequest{}

	c, err := client.FindAllBranchNames(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if grpc.Code(recvError) != codes.InvalidArgument {
		t.Fatal(recvError)
	}
}

func TestInvalidRepoFindAllBranchNamesRequest(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	repo := &pb.Repository{StorageName: "default", RelativePath: "made/up/path"}
	rpcRequest := &pb.FindAllBranchNamesRequest{Repository: repo}

	c, err := client.FindAllBranchNames(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if grpc.Code(recvError) != codes.NotFound {
		t.Fatal(recvError)
	}
}

func TestSuccessfulFindAllTagNames(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	rpcRequest := &pb.FindAllTagNamesRequest{Repository: testRepo}

	c, err := client.FindAllTagNames(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	var names [][]byte
	for {
		r, err := c.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, r.GetNames()...)
	}

	for _, tag := range []string{"v1.0.0", "v1.1.0"} {
		if !containsRef(names, "refs/tags/"+tag) {
			t.Fatal("Expected to find tag", tag, "in all tag names")
		}
	}
}

func TestEmptyFindAllTagNamesRequest(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	rpcRequest := &pb.FindAllTagNamesRequest{}

	c, err := client.FindAllTagNames(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if grpc.Code(recvError) != codes.InvalidArgument {
		t.Fatal(recvError)
	}
}

func TestInvalidRepoFindAllTagNamesRequest(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	repo := &pb.Repository{StorageName: "default", RelativePath: "made/up/path"}
	rpcRequest := &pb.FindAllTagNamesRequest{Repository: repo}

	c, err := client.FindAllTagNames(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if grpc.Code(recvError) != codes.NotFound {
		t.Fatal(recvError)
	}
}

func TestHeadReference(t *testing.T) {
	headRef, err := headReference(context.Background(), testRepoPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(headRef) != "refs/heads/master" {
		t.Fatal("Expected HEAD reference to be 'ref/heads/master', got '", string(headRef), "'")
	}
}

func TestHeadReferenceWithNonExistingHead(t *testing.T) {
	// Write bad HEAD
	ioutil.WriteFile(testRepoPath+"/HEAD", []byte("ref: refs/heads/nonexisting"), 0644)
	defer func() {
		// Restore HEAD
		ioutil.WriteFile(testRepoPath+"/HEAD", []byte("ref: refs/heads/master"), 0644)
	}()

	headRef, err := headReference(context.Background(), testRepoPath)
	if err != nil {
		t.Fatal(err)
	}
	if headRef != nil {
		t.Fatal("Expected HEAD reference to be nil, got '", string(headRef), "'")
	}
}

func TestDefaultBranchName(t *testing.T) {
	// We are going to override these functions during this test. Restore them after we're done
	defer func() {
		FindBranchNames = _findBranchNames
		headReference = _headReference
	}()

	testCases := []struct {
		desc            string
		findBranchNames func(context.Context, string) ([][]byte, error)
		headReference   func(context.Context, string) ([]byte, error)
		expected        []byte
	}{
		{
			desc:     "Get first branch when only one branch exists",
			expected: []byte("refs/heads/foo"),
			findBranchNames: func(context.Context, string) ([][]byte, error) {
				return [][]byte{[]byte("refs/heads/foo")}, nil
			},
			headReference: func(context.Context, string) ([]byte, error) { return nil, nil },
		},
		{
			desc:            "Get empy ref if no branches exists",
			expected:        nil,
			findBranchNames: func(context.Context, string) ([][]byte, error) { return [][]byte{}, nil },
			headReference:   func(context.Context, string) ([]byte, error) { return nil, nil },
		},
		{
			desc:     "Get the name of the head reference when more than one branch exists",
			expected: []byte("refs/heads/bar"),
			findBranchNames: func(context.Context, string) ([][]byte, error) {
				return [][]byte{[]byte("refs/heads/foo"), []byte("refs/heads/bar")}, nil
			},
			headReference: func(context.Context, string) ([]byte, error) { return []byte("refs/heads/bar"), nil },
		},
		{
			desc:     "Get `ref/heads/master` when several branches exist",
			expected: []byte("refs/heads/master"),
			findBranchNames: func(context.Context, string) ([][]byte, error) {
				return [][]byte{[]byte("refs/heads/foo"), []byte("refs/heads/master"), []byte("refs/heads/bar")}, nil
			},
			headReference: func(context.Context, string) ([]byte, error) { return nil, nil },
		},
		{
			desc:     "Get the name of the first branch when several branches exists and no other conditions are met",
			expected: []byte("refs/heads/foo"),
			findBranchNames: func(context.Context, string) ([][]byte, error) {
				return [][]byte{[]byte("refs/heads/foo"), []byte("refs/heads/bar"), []byte("refs/heads/baz")}, nil
			},
			headReference: func(context.Context, string) ([]byte, error) { return nil, nil },
		},
	}

	for _, testCase := range testCases {
		FindBranchNames = testCase.findBranchNames
		headReference = testCase.headReference

		defaultBranch, err := DefaultBranchName(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(defaultBranch, testCase.expected) {
			t.Fatalf("%s: expected %s, got %s instead", testCase.desc, testCase.expected, defaultBranch)
		}
	}
}

func TestSuccessfulFindDefaultBranchName(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	rpcRequest := &pb.FindDefaultBranchNameRequest{Repository: testRepo}

	r, err := client.FindDefaultBranchName(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	if name := r.GetName(); string(name) != "refs/heads/master" {
		t.Fatal("Expected HEAD reference to be 'ref/heads/master', got '", string(name), "'")
	}
}

func TestEmptyFindDefaultBranchNameRequest(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	rpcRequest := &pb.FindDefaultBranchNameRequest{}

	_, err := client.FindDefaultBranchName(context.Background(), rpcRequest)

	if grpc.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}

func TestInvalidRepoFindDefaultBranchNameRequest(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	repo := &pb.Repository{StorageName: "default", RelativePath: "/made/up/path"}
	rpcRequest := &pb.FindDefaultBranchNameRequest{Repository: repo}

	_, err := client.FindDefaultBranchName(context.Background(), rpcRequest)

	if grpc.Code(err) != codes.NotFound {
		t.Fatal(err)
	}
}

func TestSuccessfulFindLocalBranches(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	rpcRequest := &pb.FindLocalBranchesRequest{Repository: testRepo}

	c, err := client.FindLocalBranches(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	var branches []*pb.FindLocalBranchResponse
	for {
		r, err := c.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		branches = append(branches, r.GetBranches()...)
	}

	for name, target := range localBranches {
		localBranch := &pb.FindLocalBranchResponse{
			Name:          []byte(name),
			CommitId:      target.Id,
			CommitSubject: target.Subject,
			CommitAuthor: &pb.FindLocalBranchCommitAuthor{
				Name:  target.Author.Name,
				Email: target.Author.Email,
				Date:  target.Author.Date,
			},
			CommitCommitter: &pb.FindLocalBranchCommitAuthor{
				Name:  target.Committer.Name,
				Email: target.Committer.Email,
				Date:  target.Committer.Date,
			},
		}
		assertContainsLocalBranch(t, branches, localBranch)
	}
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
	testCases := []struct {
		desc          string
		relativeOrder []string
		sortBy        pb.FindLocalBranchesRequest_SortBy
	}{
		{
			desc:          "In ascending order by name",
			relativeOrder: []string{"refs/heads/'test'", "refs/heads/100%branch", "refs/heads/improve/awesome", "refs/heads/master"},
			sortBy:        pb.FindLocalBranchesRequest_NAME,
		},
		{
			desc:          "In ascending order by commiter date",
			relativeOrder: []string{"refs/heads/improve/awesome", "refs/heads/'test'", "refs/heads/100%branch", "refs/heads/master"},
			sortBy:        pb.FindLocalBranchesRequest_UPDATED_ASC,
		},
		{
			desc:          "In descending order by commiter date",
			relativeOrder: []string{"refs/heads/master", "refs/heads/100%branch", "refs/heads/'test'", "refs/heads/improve/awesome"},
			sortBy:        pb.FindLocalBranchesRequest_UPDATED_DESC,
		},
	}

	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)

	for _, testCase := range testCases {
		rpcRequest := &pb.FindLocalBranchesRequest{Repository: testRepo, SortBy: testCase.sortBy}

		c, err := client.FindLocalBranches(context.Background(), rpcRequest)
		if err != nil {
			t.Fatal(err)
		}

		var branches []string
		for {
			r, err := c.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, branch := range r.GetBranches() {
				branches = append(branches, string(branch.Name))
			}
		}

		if !isOrderedSubset(testCase.relativeOrder, branches) {
			t.Fatalf("%s: Expected branches to have relative order %v; got them as %v", testCase.desc, testCase.relativeOrder, branches)
		}
	}
}

func TestEmptyFindLocalBranchesRequest(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	rpcRequest := &pb.FindLocalBranchesRequest{}

	c, err := client.FindLocalBranches(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}

	var recvError error
	for recvError == nil {
		_, recvError = c.Recv()
	}

	if grpc.Code(recvError) != codes.InvalidArgument {
		t.Fatal(recvError)
	}
}

func deleteRemoteBranch(t *testing.T, repoPath, remoteName, branchName string) {
	testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "update-ref", "-d",
		"refs/remotes/"+remoteName+"/"+branchName)
}

func createRemoteBranch(t *testing.T, repoPath, remoteName, branchName, ref string) {
	testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "update-ref",
		"refs/remotes/"+remoteName+"/"+branchName, ref)
}

func TestSuccessfulFindAllBranchesRequest(t *testing.T) {
	server := runRefServiceServer(t)
	defer server.Stop()

	remoteBranch := &pb.FindAllBranchesResponse_Branch{
		Name: []byte("refs/remotes/origin/fake-remote-branch"),
		Target: &pb.GitCommit{
			Id:      "913c66a37b4a45b9769037c55c2d238bd0942d2e",
			Subject: []byte("Files, encoding and much more"),
			Author: &pb.CommitAuthor{
				Name:  []byte("Dmitriy Zaporozhets"),
				Email: []byte("<dmitriy.zaporozhets@gmail.com>"),
				Date:  &timestamp.Timestamp{Seconds: 1393488896},
			},
			Committer: &pb.CommitAuthor{
				Name:  []byte("Dmitriy Zaporozhets"),
				Email: []byte("<dmitriy.zaporozhets@gmail.com>"),
				Date:  &timestamp.Timestamp{Seconds: 1393488896},
			},
		},
	}

	createRemoteBranch(t, testRepoPath, "origin", "fake-remote-branch",
		remoteBranch.Target.Id)
	defer deleteRemoteBranch(t, testRepoPath, "origin", "fake-remote-branch")

	request := &pb.FindAllBranchesRequest{Repository: testRepo}
	client := newRefServiceClient(t)
	c, err := client.FindAllBranches(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	var branches []*pb.FindAllBranchesResponse_Branch
	for {
		r, err := c.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		branches = append(branches, r.GetBranches()...)
	}

	// It contains local branches
	for name, target := range localBranches {
		branch := &pb.FindAllBranchesResponse_Branch{
			Name:   []byte(name),
			Target: target,
		}
		assertContainsBranch(t, branches, branch)
	}

	// It contains our fake remote branch
	assertContainsBranch(t, branches, remoteBranch)
}

func TestInvalidFindAllBranchesRequest(t *testing.T) {
	server := runRefServiceServer(t)
	defer server.Stop()

	client := newRefServiceClient(t)
	testCases := []struct {
		description string
		request     pb.FindAllBranchesRequest
	}{
		{
			description: "Empty request",
			request:     pb.FindAllBranchesRequest{},
		},
		{
			description: "Invalid repo",
			request: pb.FindAllBranchesRequest{
				Repository: &pb.Repository{
					StorageName:  "fake",
					RelativePath: "repo",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Logf("test case: %v", tc.description)

		c, err := client.FindAllBranches(context.Background(), &tc.request)
		if err != nil {
			t.Fatal(err)
		}

		var recvError error
		for recvError == nil {
			_, recvError = c.Recv()
		}

		testhelper.AssertGrpcError(t, recvError, codes.InvalidArgument, "")
	}
}
