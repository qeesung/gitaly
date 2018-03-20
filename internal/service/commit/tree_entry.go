package commit

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/streamio"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func treeEntryHandler(stream pb.Commit_TreeEntryServer, c *catfile.C, revision, path string, limit int64) catfile.Handler {
	return func(stdin io.Writer, stdout *bufio.Reader) error {
		treeEntry, err := TreeEntryForRevisionAndPath(c, revision, path)
		if err != nil {
			return err
		}

		if treeEntry == nil || len(treeEntry.Oid) == 0 {
			return helper.DecorateError(codes.Unavailable, stream.Send(&pb.TreeEntryResponse{}))
		}

		if treeEntry.Type == pb.TreeEntry_COMMIT {
			response := &pb.TreeEntryResponse{
				Type: pb.TreeEntryResponse_COMMIT,
				Mode: treeEntry.Mode,
				Oid:  treeEntry.Oid,
			}
			if err := stream.Send(response); err != nil {
				return status.Errorf(codes.Unavailable, "TreeEntry: send: %v", err)
			}

			return nil
		}

		stdin.Write([]byte(treeEntry.Oid + "\n"))

		objectInfo, err := catfile.ParseObjectInfo(stdout)
		if err != nil {
			return status.Errorf(codes.Internal, "TreeEntry: %v", err)
		}

		if strings.ToLower(treeEntry.Type.String()) != objectInfo.Type {
			return status.Errorf(
				codes.Internal,
				"TreeEntry: mismatched object type: tree-oid=%s object-oid=%s entry-type=%s object-type=%s",
				treeEntry.Oid, objectInfo.Oid, treeEntry.Type.String(), objectInfo.Type,
			)
		}

		if objectInfo.Type == "tree" {
			response := &pb.TreeEntryResponse{
				Type: pb.TreeEntryResponse_TREE,
				Oid:  objectInfo.Oid,
				Size: objectInfo.Size,
				Mode: treeEntry.Mode,
			}
			return helper.DecorateError(codes.Unavailable, stream.Send(response))
		}

		dataLength := objectInfo.Size
		if limit > 0 && dataLength > limit {
			dataLength = limit
		}

		response := &pb.TreeEntryResponse{
			Type: pb.TreeEntryResponse_BLOB,
			Oid:  objectInfo.Oid,
			Size: objectInfo.Size,
			Mode: treeEntry.Mode,
		}
		if dataLength == 0 {
			return helper.DecorateError(codes.Unavailable, stream.Send(response))
		}

		sw := streamio.NewWriter(func(p []byte) error {
			response.Data = p

			if err := stream.Send(response); err != nil {
				return status.Errorf(codes.Unavailable, "TreeEntry: send: %v", err)
			}

			// Use a new response so we don't send other fields (Size, ...) over and over
			response = &pb.TreeEntryResponse{}

			return nil
		})

		n, err := io.Copy(sw, io.LimitReader(stdout, dataLength))
		if n < dataLength && err == nil {
			return status.Errorf(codes.Internal, "TreeEntry: Incomplete copy")
		}

		return err
	}
}

func (s *server) TreeEntry(in *pb.TreeEntryRequest, stream pb.CommitService_TreeEntryServer) error {
	if err := validateRequest(in); err != nil {
		return status.Errorf(codes.InvalidArgument, "TreeEntry: %v", err)
	}

	requestPath := string(in.GetPath())
	// path.Dir("api/docs") => "api" Correct!
	// path.Dir("api/docs/") => "api/docs" WRONG!
	if len(requestPath) > 1 {
		requestPath = strings.TrimRight(requestPath, "/")
	}

	c, err := catfile.New(stream.Context(), in.Repository)
	if err != nil {
		return err
	}

	handler := treeEntryHandler(stream, c, string(in.GetRevision()), requestPath, in.GetLimit())
	return catfile.CatFile(stream.Context(), in.Repository, handler)
}

func validateRequest(in *pb.TreeEntryRequest) error {
	if len(in.GetRevision()) == 0 {
		return fmt.Errorf("empty Revision")
	}

	if len(in.GetPath()) == 0 {
		return fmt.Errorf("empty Path")
	}

	return nil
}
