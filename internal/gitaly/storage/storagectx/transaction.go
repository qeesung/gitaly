package storagectx

import (
	"context"
	"errors"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	housekeepingcfg "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	grpc_metadata "google.golang.org/grpc/metadata"
)

// Transaction is the interface of the storagemgr.Transaction accessible through the context.
// See the details of that type for method documentation.
type Transaction interface {
	MarkDefaultBranchUpdated()
	DeleteRepository()
	MarkCustomHooksUpdated()
	IncludeObject(git.ObjectID)
	OriginalRepository(*gitalypb.Repository) *gitalypb.Repository
	MarkAlternateUpdated()
	PackRefs()
	Repack(housekeepingcfg.RepackObjectsConfig)
	WriteCommitGraphs(housekeepingcfg.WriteCommitGraphConfig)
	SnapshotLSN() storage.LSN
	Root() string
	Commit(context.Context) error
}

type keyTransaction struct{}

// ContextWithTransaction stores the transaction into the context.
func ContextWithTransaction(ctx context.Context, tx Transaction) context.Context {
	return context.WithValue(ctx, keyTransaction{}, tx)
}

// RunWithTransaction runs the callback with the transaction in the context. If there is
// no transaction in the context, the callback is not ran.
func RunWithTransaction(ctx context.Context, callback func(tx Transaction)) {
	value := ctx.Value(keyTransaction{})
	if value == nil {
		return
	}

	callback(value.(Transaction))
}

const keyPartitioningHint = "gitaly-partitioning-hint"

// SetPartitioningHintToIncomingContext stores the relativePath as a partitioning hint into the incoming
// gRPC metadata in the context.
func SetPartitioningHintToIncomingContext(ctx context.Context, relativePath string) context.Context {
	md, ok := grpc_metadata.FromIncomingContext(ctx)
	if !ok {
		md = grpc_metadata.New(nil)
	} else {
		md = md.Copy()
	}
	md.Set(keyPartitioningHint, relativePath)

	return grpc_metadata.NewIncomingContext(ctx, md)
}

// ExtractPartitioningHintFromIncomingContext extracts the partitioning hint from the outgoing gRPC
// metadata in the context. Empty string is returned if no partitoning hint was provided.
func ExtractPartitioningHintFromIncomingContext(ctx context.Context) (string, error) {
	relativePaths := grpc_metadata.ValueFromIncomingContext(ctx, keyPartitioningHint)
	if len(relativePaths) > 1 {
		return "", errors.New("multiple partitioning hints")
	}

	if len(relativePaths) == 0 {
		// No partitioning hint was set.
		return "", nil
	}

	return relativePaths[0], nil
}

// RemovePartitioningHintFromIncomingContext removes the partitioning hint from the provided context.
func RemovePartitioningHintFromIncomingContext(ctx context.Context) context.Context {
	md, ok := grpc_metadata.FromIncomingContext(ctx)
	if !ok {
		md = grpc_metadata.New(nil)
	} else {
		md = md.Copy()
	}
	md.Delete(keyPartitioningHint)

	return grpc_metadata.NewIncomingContext(ctx, md)
}
