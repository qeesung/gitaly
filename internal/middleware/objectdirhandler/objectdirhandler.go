package objectdirhandler

import (
	"sync"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

type requestWithRepository interface {
	GetRepository() *pb.Repository
}

type ctxObjectDirMarker struct{}
type ctxAltObjectDirsMarker struct{}

var (
	ctxObjectDirMarkerKey     = &ctxObjectDirMarker{}
	ctxAltObjectDirsMarkerKey = &ctxAltObjectDirsMarker{}
)

type recvWrapper struct {
	grpc.ServerStream
	wrappedContext context.Context
	wrapOnce       sync.Once
}

// Unary sets Git object dir attributes for unary RPCs
func Unary(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return handler(newContextWithDirValues(ctx, req), req)
}

// Stream sets Git object dir attributes for streaming RPCs
func Stream(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return handler(srv, &recvWrapper{ServerStream: stream, wrappedContext: stream.Context()})
}

// ObjectDir returns the value for Repository.GitObjectDirectory
func ObjectDir(ctx context.Context) (string, bool) {
	dir1 := ctx.Value(ctxObjectDirMarkerKey)
	if dir1 == nil {
		return "", false
	}

	dir2, ok := dir1.(string)
	return dir2, ok
}

// AltObjectDirs returns the value for Repository.GitAlternateObjectDirectories
func AltObjectDirs(ctx context.Context) ([]string, bool) {
	dirs1 := ctx.Value(ctxAltObjectDirsMarkerKey)
	if dirs1 == nil {
		return nil, false
	}

	dirs2, ok := dirs1.([]string)
	return dirs2, ok
}

func newContextWithDirValues(ctx context.Context, req interface{}) context.Context {
	if repo, ok := req.(requestWithRepository); ok {
		if dir := repo.GetRepository().GetGitObjectDirectory(); dir != "" {
			ctx = context.WithValue(ctx, ctxObjectDirMarkerKey, dir)
		}

		if dirs := repo.GetRepository().GetGitAlternateObjectDirectories(); len(dirs) > 0 {
			ctx = context.WithValue(ctx, ctxAltObjectDirsMarkerKey, dirs)
		}
	}

	return ctx
}

func (s *recvWrapper) RecvMsg(m interface{}) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}

	s.wrapOnce.Do(func() {
		s.wrappedContext = newContextWithDirValues(s.wrappedContext, m)
	})

	return nil
}

func (s *recvWrapper) Context() context.Context {
	return s.wrappedContext
}
