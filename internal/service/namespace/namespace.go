package namespace

import (
	"os"
	"path"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var noNameError = status.Errorf(codes.InvalidArgument, "Name: cannot be empty")

func (s *server) NamespaceExists(ctx context.Context, in *pb.NamespaceExistsRequest) (*pb.NamespaceExistsResponse, error) {
	storagePath, err := helper.GetStorageByName(in.GetStorageName())
	if err != nil {
		return nil, err
	}

	// This case should return an error, as else we'd actually say the path exists as the
	// storage exists
	if in.GetName() == "" {
		return nil, noNameError
	}

	if fi, err := os.Stat(namespacePath(storagePath, in.GetName())); os.IsNotExist(err) {
		return &pb.NamespaceExistsResponse{Exists: false}, nil
	} else if err != nil {
		return nil, status.Errorf(codes.Internal, "could not stat the directory: %v", err)
	} else {
		return &pb.NamespaceExistsResponse{Exists: fi.IsDir()}, nil
	}
}

func (s *server) AddNamespace(ctx context.Context, in *pb.AddNamespaceRequest) (*pb.AddNamespaceResponse, error) {
	storagePath, err := helper.GetStorageByName(in.GetStorageName())
	if err != nil {
		return nil, err
	}

	// Make idempotent, as it's called through Sidekiq
	// Exists check will return an err if in.GetName() == ""
	existsRequest := &pb.NamespaceExistsRequest{StorageName: in.StorageName, Name: in.Name}
	if exists, err := s.NamespaceExists(ctx, existsRequest); err != nil {
		return nil, err
	} else if exists.Exists {
		return &pb.AddNamespaceResponse{}, nil
	}

	if err = os.MkdirAll(namespacePath(storagePath, in.GetName()), 0770); err != nil {
		return nil, status.Errorf(codes.Internal, "create directory: %v", err)
	}

	return &pb.AddNamespaceResponse{}, nil
}

func (s *server) RenameNamespace(ctx context.Context, in *pb.RenameNamespaceRequest) (*pb.RenameNamespaceResponse, error) {
	storagePath, err := helper.GetStorageByName(in.GetStorageName())
	if err != nil {
		return nil, err
	}

	if in.GetFrom() == "" || in.GetTo() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "from and to cannot be empty")
	}

	// No need to check if the from path exists, if it doesn't, we'd later get an
	// os.LinkError
	toExistsCheck := &pb.NamespaceExistsRequest{StorageName: in.StorageName, Name: in.GetTo()}
	if exists, err := s.NamespaceExists(ctx, toExistsCheck); err != nil {
		return nil, err
	} else if exists.Exists {
		return nil, status.Errorf(codes.InvalidArgument, "to directory %s already exists", in.GetTo())
	}

	err = os.Rename(namespacePath(storagePath, in.GetFrom()), namespacePath(storagePath, in.GetTo()))
	if _, ok := err.(*os.LinkError); ok {
		return nil, status.Errorf(codes.InvalidArgument, "from directory %s not found", in.GetFrom())
	} else if err != nil {
		return nil, status.Errorf(codes.Internal, "rename: %v", err)
	}

	return &pb.RenameNamespaceResponse{}, nil
}

func (s *server) RemoveNamespace(ctx context.Context, in *pb.RemoveNamespaceRequest) (*pb.RemoveNamespaceResponse, error) {
	storagePath, err := helper.GetStorageByName(in.GetStorageName())
	if err != nil {
		return nil, err
	}

	// Needed as else we might destroy the whole storage
	if in.GetName() == "" {
		return nil, noNameError
	}

	// os.RemoveAll is idempotent by itself
	// No need to check if the directory exists, or not
	if err = os.RemoveAll(namespacePath(storagePath, in.GetName())); err != nil {
		return nil, status.Errorf(codes.Internal, "removal: %v", err)
	}
	return &pb.RemoveNamespaceResponse{}, nil
}

func namespacePath(storage, ns string) string {
	return path.Join(storage, ns)
}
