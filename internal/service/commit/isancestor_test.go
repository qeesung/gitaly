package commit

import (
	"testing"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

func TestCommitIsAncestorFailure(t *testing.T) {
	service, ruby, serverSocketPath := startTestServices(t)
	defer stopTestServices(service, ruby)

	client := newCommitServiceClient(t, serverSocketPath)

	queries := []struct {
		Request   *pb.CommitIsAncestorRequest
		ErrorCode codes.Code
		ErrMsg    string
	}{
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: nil,
				AncestorId: "b83d6e391c22777fca1ed3012fce84f633d7fed0",
				ChildId:    "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab",
			},
			ErrorCode: codes.InvalidArgument,
			ErrMsg:    "Expected to throw invalid argument got: %s",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "",
				ChildId:    "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab",
			},
			ErrorCode: codes.InvalidArgument,
			ErrMsg:    "Expected to throw invalid argument got: %s",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "b83d6e391c22777fca1ed3012fce84f633d7fed0",
				ChildId:    "",
			},
			ErrorCode: codes.InvalidArgument,
			ErrMsg:    "Expected to throw invalid argument got: %s",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: &pb.Repository{StorageName: "default", RelativePath: "fake-path"},
				AncestorId: "b83d6e391c22777fca1ed3012fce84f633d7fed0",
				ChildId:    "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab",
			},
			ErrorCode: codes.NotFound,
			ErrMsg:    "Expected to throw internal got: %s",
		},
	}

	for _, v := range queries {
		if _, err := client.CommitIsAncestor(context.Background(), v.Request); err == nil {
			t.Error("Expected to throw an error")
		} else if grpc.Code(err) != v.ErrorCode {
			t.Errorf(v.ErrMsg, err)
		}
	}
}

func TestCommitIsAncestorSuccess(t *testing.T) {
	service, ruby, serverSocketPath := startTestServices(t)
	defer stopTestServices(service, ruby)

	client := newCommitServiceClient(t, serverSocketPath)

	queries := []struct {
		Request  *pb.CommitIsAncestorRequest
		Response bool
		ErrMsg   string
	}{
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab",
				ChildId:    "372ab6950519549b14d220271ee2322caa44d4eb",
			},
			Response: true,
			ErrMsg:   "Expected commit to be ancestor",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "b83d6e391c22777fca1ed3012fce84f633d7fed0",
				ChildId:    "38008cb17ce1466d8fec2dfa6f6ab8dcfe5cf49e",
			},
			Response: false,
			ErrMsg:   "Expected commit not to be ancestor",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "1234123412341234123412341234123412341234",
				ChildId:    "b83d6e391c22777fca1ed3012fce84f633d7fed0",
			},
			Response: false,
			ErrMsg:   "Expected invalid commit to not be ancestor",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "b83d6e391c22777fca1ed3012fce84f633d7fed0",
				ChildId:    "gitaly-stuff",
			},
			Response: true,
			ErrMsg:   "Expected `b83d6e391c22777fca1ed3012fce84f633d7fed0` to be ancestor of `gitaly-stuff`",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "gitaly-stuff",
				ChildId:    "master",
			},
			Response: false,
			ErrMsg:   "Expected branch `gitaly-stuff` not to be ancestor of `master`",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "refs/tags/v1.0.0",
				ChildId:    "refs/tags/v1.1.0",
			},
			Response: true,
			ErrMsg:   "Expected tag `v1.0.0` to be ancestor of `v1.1.0`",
		},
		{
			Request: &pb.CommitIsAncestorRequest{
				Repository: testRepo,
				AncestorId: "refs/tags/v1.1.0",
				ChildId:    "refs/tags/v1.0.0",
			},
			Response: false,
			ErrMsg:   "Expected branch `v1.1.0` not to be ancestor of `v1.0.0`",
		},
	}

	for _, v := range queries {
		c, err := client.CommitIsAncestor(context.Background(), v.Request)
		if err != nil {
			t.Fatalf("CommitIsAncestor threw error unexpectedly: %v", err)
		}

		response := c.GetValue()
		if response != v.Response {
			t.Errorf(v.ErrMsg)
		}
	}
}
