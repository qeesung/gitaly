package git

import (
	"context"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
)

// InternalReferenceType is the type of an internal reference.
type InternalReferenceType int

const (
	// InternalReferenceTypeHidden indicates that a reference should never be advertised or
	// writeable.
	InternalReferenceTypeHidden = InternalReferenceType(iota + 1)
	// InternalReferenceTypeReadonly indicates that a reference should be advertised, but not
	// writeable.
	InternalReferenceTypeReadonly
)

// InternalRefPrefixes is an array of all reference prefixes which are used internally by GitLab.
// These need special treatment in some cases, e.g. to restrict writing to them.
var InternalRefPrefixes = map[string]InternalReferenceType{
	// Environments may be interesting to the user in case they want to figure out what exact
	// reference an environment has been constructed from.
	"refs/environments/": InternalReferenceTypeReadonly,

	// Keep-around references are only used internally to keep alive objects, and thus they
	// shouldn't be exposed to the user.
	"refs/keep-around/": InternalReferenceTypeHidden,

	// Merge request references should be readable by the user so that they can still fetch the
	// changes of specific merge requests.
	"refs/merge-requests/": InternalReferenceTypeReadonly,

	// Pipelines may be interesting to the user in case they want to figure out what exact
	// reference a specific pipeline has been running with.
	"refs/pipelines/": InternalReferenceTypeReadonly,

	// Remote references shouldn't typically exist in repositories nowadays anymore, and there
	// is no reason to expose them to the user.
	"refs/remotes/": InternalReferenceTypeHidden,

	// Temporary references are used internally by Rails for various operations and should not
	// be exposed to the user.
	"refs/tmp/": InternalReferenceTypeHidden,
}

// ObjectPoolRefNamespace is the namespace used for the references of the primary pool member part
// of an object pool.
const ObjectPoolRefNamespace = "refs/remotes/origin"

// Revision represents anything that resolves to either a commit, multiple
// commits or to an object different than a commit. This could be e.g.
// "master", "master^{commit}", an object hash or similar. See gitrevisions(1)
// for supported syntax.
type Revision string

// String returns the string representation of the Revision.
func (r Revision) String() string {
	return string(r)
}

// ReferenceName represents the name of a git reference, e.g.
// "refs/heads/master". It does not support extended revision notation like a
// Revision does and must always contain a fully qualified reference.
type ReferenceName string

// NewReferenceNameFromBranchName returns a new ReferenceName from a given
// branch name. Note that branch is treated as an unqualified branch name.
// This function will thus always prepend "refs/heads/".
func NewReferenceNameFromBranchName(branch string) ReferenceName {
	return ReferenceName("refs/heads/" + branch)
}

// String returns the string representation of the ReferenceName.
func (r ReferenceName) String() string {
	return string(r)
}

// Revision converts the ReferenceName to a Revision. This is safe to do as a
// reference is always also a revision.
func (r ReferenceName) Revision() Revision {
	return Revision(r)
}

// Branch returns `true` and the branch name if the reference is a branch. E.g.
// if ReferenceName is "refs/heads/master", it will return "master". If it is
// not a branch, `false` is returned.
func (r ReferenceName) Branch() (string, bool) {
	if strings.HasPrefix(r.String(), "refs/heads/") {
		return r.String()[len("refs/heads/"):], true
	}
	return "", false
}

// Reference represents a Git reference.
type Reference struct {
	// Name is the name of the reference
	Name ReferenceName
	// Target is the target of the reference. For direct references it
	// contains the object ID, for symbolic references it contains the
	// target branch name.
	Target string
	// IsSymbolic tells whether the reference is direct or symbolic
	IsSymbolic bool
}

// NewReference creates a direct reference to an object.
func NewReference(name ReferenceName, target string) Reference {
	return Reference{
		Name:       name,
		Target:     target,
		IsSymbolic: false,
	}
}

// NewSymbolicReference creates a symbolic reference to another reference.
func NewSymbolicReference(name ReferenceName, target string) Reference {
	return Reference{
		Name:       name,
		Target:     target,
		IsSymbolic: true,
	}
}

// CheckRefFormat checks whether a fully-qualified refname is well
// well-formed using git-check-ref-format
func CheckRefFormat(ctx context.Context, gitCmdFactory CommandFactory, refName string) (bool, error) {
	cmd, err := gitCmdFactory.NewWithoutRepo(ctx,
		SubCmd{
			Name: "check-ref-format",
			Args: []string{refName},
		},
		WithStdout(io.Discard),
		WithStderr(io.Discard),
	)
	if err != nil {
		return false, err
	}

	if err := cmd.Wait(); err != nil {
		if code, ok := command.ExitStatus(err); ok && code == 1 {
			return false, nil
		}

		return false, err
	}

	return true, nil
}
