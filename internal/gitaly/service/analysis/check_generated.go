package analysis

import (
	"errors"
	"fmt"
	"io"

	"github.com/go-enry/go-enry/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

func (s *server) CheckBlobsGenerated(stream gitalypb.AnalysisService_CheckBlobsGeneratedServer) (returnedErr error) {
	req, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receiving first request: %w", err)
	}

	repository := req.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	ctx := stream.Context()
	repo := s.localrepo(repository)

	reader, readerCancel, err := s.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return fmt.Errorf("retrieving object reader: %w", err)
	}
	defer readerCancel()

	queue, queueCancel, err := reader.Queue(ctx)
	if err != nil {
		return fmt.Errorf("retrieving object queue: %w", err)
	}
	defer queueCancel()

	group, groupCtx := errgroup.WithContext(ctx)
	requestsChan := make(chan *gitalypb.CheckBlobsGeneratedRequest)

	// The output of git-cat-file(1) is processed in a separate goroutine to allow requests to
	// continuously queue additional objects for processing.
	group.Go(func() error {
		chunkSender := chunk.New(&checkBlobsGeneratedSender{stream: stream})

		for req := range requestsChan {
			for _, blob := range req.GetBlobs() {
				object, err := queue.ReadObject(ctx)
				if err != nil {
					return fmt.Errorf("reading object: %w", err)
				}

				// The requested Git revisions must always resolve to a blob. Otherwise, there is
				// not a file to perform the generation check on.
				if !object.IsBlob() {
					return structerr.NewInvalidArgument("object is not a blob")
				}

				// Read an arbitrary number of bytes that is considered enough to determine whether
				// the file is generated.
				content, err := io.ReadAll(io.LimitReader(object, 2048))
				if err != nil {
					return fmt.Errorf("reading blob content: %w", err)
				}

				// Any remaining blob data must be consumed before reading the next object. This is
				// quite inefficient, but there is currently no alternative because git-cat-file(1)
				// cannot be asked to limit the number of bytes it's outputting.
				if _, err := io.Copy(io.Discard, object); err != nil {
					return fmt.Errorf("discarding remaining blob content: %w", err)
				}

				if err := chunkSender.Send(&gitalypb.CheckBlobsGeneratedResponse_Blob{
					Revision:  blob.Revision,
					Generated: enry.IsGenerated(string(blob.Path), content),
				}); err != nil {
					return fmt.Errorf("sending response: %w", err)
				}
			}

			// The sender is flushed for each received request message so that at least one response
			// message is always produced.
			if err := chunkSender.Flush(); err != nil {
				return fmt.Errorf("flushing response: %w", err)
			}
		}

		return nil
	})

	// Ensure that the sending goroutine always closes and any lingering requests are first
	// processed before the surrounding function returns.
	defer func() {
		close(requestsChan)
		if err := group.Wait(); err != nil && returnedErr == nil {
			returnedErr = err
		}
	}()

	for {
		if err := validateCheckBlobsGeneratedRequest(req); err != nil {
			return structerr.NewInvalidArgument("validating request: %w", err)
		}

		// Queue up all revisions specified in the request for processing through git-cat-file(1).
		for _, blob := range req.GetBlobs() {
			if err := queue.RequestObject(ctx, git.Revision(blob.Revision)); err != nil {
				return fmt.Errorf("requesting object: %w", err)
			}
		}

		if err := queue.Flush(ctx); err != nil {
			return fmt.Errorf("flushing queue: %w", err)
		}

		select {
		// When performing the file generation check the file path is used to gain additional
		// insight. Send the request to the processing goroutine to provide file paths and context
		// for how to batch response messages.
		case requestsChan <- req:
		// The group context is cancelled when the sending goroutine exits with an error.
		case <-groupCtx.Done():
			return nil
		}

		req, err = stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("receiving next request: %w", err)
		}
	}

	return nil
}

func validateCheckBlobsGeneratedRequest(req *gitalypb.CheckBlobsGeneratedRequest) error {
	if len(req.Blobs) == 0 {
		return errors.New("empty blobs")
	}

	for _, blob := range req.Blobs {
		if err := git.ValidateRevision(blob.Revision, git.AllowPathScopedRevision()); err != nil {
			return err
		}

		if len(blob.GetPath()) == 0 {
			return errors.New("empty path")
		}
	}

	return nil
}

type checkBlobsGeneratedSender struct {
	stream   gitalypb.AnalysisService_CheckBlobsGeneratedServer
	response *gitalypb.CheckBlobsGeneratedResponse
}

func (s *checkBlobsGeneratedSender) Reset() {
	s.response = &gitalypb.CheckBlobsGeneratedResponse{}
}

func (s *checkBlobsGeneratedSender) Append(m proto.Message) {
	s.response.Blobs = append(s.response.Blobs, m.(*gitalypb.CheckBlobsGeneratedResponse_Blob))
}

func (s *checkBlobsGeneratedSender) Send() error {
	return s.stream.Send(s.response)
}
