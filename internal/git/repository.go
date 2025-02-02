package git

import (
	"context"
	"errors"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
)

// DefaultBranch is the default reference written to HEAD when a repository is created
const DefaultBranch = "main"

// DefaultRef is the reference that GitLab will use if HEAD of the bare repository
// is not found, or other edge cases to detect the default branch.
const DefaultRef = ReferenceName("refs/heads/" + DefaultBranch)

// LegacyDefaultRef is the reference that used to be the default HEAD of the bare
// repository. If the default reference is not found, Gitaly will still test the
// legacy default.
const LegacyDefaultRef = ReferenceName("refs/heads/master")

// MirrorRefSpec is the refspec used when --mirror is specified on git clone.
const MirrorRefSpec = "+refs/*:refs/*"

var (
	// ErrReferenceNotFound represents an error when a reference was not
	// found.
	ErrReferenceNotFound = errors.New("reference not found")
	// ErrReferenceAmbiguous represents an error when a reference couldn't
	// unambiguously be resolved.
	ErrReferenceAmbiguous = errors.New("reference is ambiguous")

	// ErrAlreadyExists represents an error when the resource is already exists.
	ErrAlreadyExists = errors.New("already exists")
	// ErrNotFound represents an error when the resource can't be found.
	ErrNotFound = errors.New("not found")
)

// Repository is the common interface of different repository implementations.
type Repository interface {
	// ResolveRevision tries to resolve the given revision to its object
	// ID. This uses the typical DWIM mechanism of git, see gitrevisions(1)
	// for accepted syntax. This will not verify whether the object ID
	// exists. To do so, you can peel the reference to a given object type,
	// e.g. by passing `refs/heads/master^{commit}`.
	ResolveRevision(ctx context.Context, revision Revision) (ObjectID, error)
	// HasBranches returns whether the repository has branches.
	HasBranches(ctx context.Context) (bool, error)
	// GetDefaultBranch returns the default branch of the repository.
	GetDefaultBranch(ctx context.Context) (ReferenceName, error)
	// HeadReference returns the reference that HEAD points to for the
	// repository.
	HeadReference(ctx context.Context) (ReferenceName, error)
}

// RepositoryExecutor is an interface which allows execution of Git commands in a specific
// repository.
type RepositoryExecutor interface {
	storage.Repository
	Exec(ctx context.Context, cmd Command, opts ...CmdOpt) (*command.Command, error)
	ExecAndWait(ctx context.Context, cmd Command, opts ...CmdOpt) error
	GitVersion(ctx context.Context) (Version, error)
	ObjectHash(ctx context.Context) (ObjectHash, error)
	ReferenceBackend(ctx context.Context) (ReferenceBackend, error)
}
