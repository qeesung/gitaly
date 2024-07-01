package cleanup

import (
	"context"
	"errors"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/protobuf/proto"
)

type bfgStreamReader struct {
	firstRequest *gitalypb.ApplyBfgObjectMapStreamRequest

	server gitalypb.CleanupService_ApplyBfgObjectMapStreamServer
}

type bfgStreamWriter struct {
	entries []*gitalypb.ApplyBfgObjectMapStreamResponse_Entry

	server gitalypb.CleanupService_ApplyBfgObjectMapStreamServer
}

func (s *server) ApplyBfgObjectMapStream(server gitalypb.CleanupService_ApplyBfgObjectMapStreamServer) error {
	firstRequest, err := server.Recv()
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	ctx := server.Context()
	if err := validateFirstRequest(ctx, s.locator, firstRequest); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(firstRequest.GetRepository())
	reader := &bfgStreamReader{firstRequest: firstRequest, server: server}
	chunker := chunk.New(&bfgStreamWriter{server: server})

	notifier, cancel, err := newNotifier(ctx, s.catfileCache, repo, chunker)
	if err != nil {
		return structerr.NewInternal("%w", err)
	}
	defer cancel()

	// It doesn't matter if new internal references are added after this RPC
	// starts running - they shouldn't point to the objects removed by the BFG
	cleaner, err := newCleaner(ctx, s.logger, repo, notifier.notify)
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	if err := cleaner.applyObjectMap(ctx, reader.streamReader()); err != nil {
		var invalidErr errInvalidObjectMap
		if errors.As(err, &invalidErr) {
			return structerr.NewInvalidArgument("%w", invalidErr)
		}

		return structerr.NewInternal("%w", err)
	}

	if err := chunker.Flush(); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func validateFirstRequest(ctx context.Context, locator storage.Locator, req *gitalypb.ApplyBfgObjectMapStreamRequest) error {
	return locator.ValidateRepository(ctx, req.GetRepository())
}

func (r *bfgStreamReader) readOne() ([]byte, error) {
	if r.firstRequest != nil {
		data := r.firstRequest.GetObjectMap()
		r.firstRequest = nil
		return data, nil
	}

	req, err := r.server.Recv()
	if err != nil {
		return nil, err
	}

	return req.GetObjectMap(), nil
}

func (r *bfgStreamReader) streamReader() io.Reader {
	return streamio.NewReader(r.readOne)
}

func (w *bfgStreamWriter) Append(m proto.Message) {
	w.entries = append(
		w.entries,
		m.(*gitalypb.ApplyBfgObjectMapStreamResponse_Entry),
	)
}

func (w *bfgStreamWriter) Reset() {
	w.entries = nil
}

func (w *bfgStreamWriter) Send() error {
	msg := &gitalypb.ApplyBfgObjectMapStreamResponse{Entries: w.entries}

	return w.server.Send(msg)
}
