package repository

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func applyGitattributesHandler(ctx context.Context, repoPath string, revision []byte) catfile.Handler {
	return func(stdin io.Writer, stdout *bufio.Reader) error {
		infoPath := path.Join(repoPath, "info")
		attributesPath := path.Join(infoPath, "attributes")

		if _, err := fmt.Fprintf(stdin, "%s\n", revision); err != nil {
			return err
		}
		revisionInfo, err := catfile.ParseObjectInfo(stdout)
		if err != nil {
			return err
		}
		if revisionInfo.Oid == "" {
			return grpc.Errorf(codes.InvalidArgument, "Revision doesn't exist")
		}
		// Discard revision info
		if _, err := stdout.Discard(int(revisionInfo.Size) + 1); err != nil {
			return fmt.Errorf("stdout discard: %v", err)
		}

		if _, err := fmt.Fprintf(stdin, "%s:%s\n", revision, ".gitattributes"); err != nil {
			return err
		}
		blobInfo, err := catfile.ParseObjectInfo(stdout)
		if err != nil {
			return err
		}
		if blobInfo.Oid == "" || blobInfo.Type != "blob" {
			// Remove info/attributes file if there's no .gitattributes file
			err := os.Remove(attributesPath)

			// Ignore error if atttributes file doesn't exist
			if err != nil && !os.IsNotExist(err) {
				return err
			}

			return nil
		}

		// Create  /info folder if it doesn't exist
		if err := os.MkdirAll(infoPath, 0755); err != nil {
			return err
		}

		tempFile, err := ioutil.TempFile(infoPath, "attributes")
		if err != nil {
			return grpc.Errorf(codes.Internal, "ApplyGitAttributes: creating temp file: %v", err)
		}
		defer os.Remove(tempFile.Name())

		// Write attributes to temp file
		limitReader := io.LimitReader(stdout, blobInfo.Size)
		n, err := io.Copy(tempFile, limitReader)
		if err != nil {
			return err
		}
		if n != blobInfo.Size {
			return grpc.Errorf(codes.Internal,
				"ApplyGitAttributes: copy yielded %v bytes, expected %v", n, blobInfo.Size)
		}

		if err := tempFile.Close(); err != nil {
			return err
		}

		// Rename temp file and return the result
		return os.Rename(tempFile.Name(), attributesPath)
	}
}

func (server) ApplyGitattributes(ctx context.Context, in *pb.ApplyGitattributesRequest) (*pb.ApplyGitattributesResponse, error) {
	repoPath, err := helper.GetRepoPath(in.GetRepository())
	if err != nil {
		return nil, err
	}

	if err := git.ValidateRevision(in.GetRevision()); err != nil {
		return nil, grpc.Errorf(codes.InvalidArgument, "ApplyGitAttributes: revision: %v", err)
	}

	handler := applyGitattributesHandler(ctx, repoPath, in.GetRevision())

	if err := catfile.CatFile(ctx, repoPath, handler); err != nil {
		return nil, err
	}

	return &pb.ApplyGitattributesResponse{}, nil
}
