//go:build gitaly_test_sha256

package gittest

import (
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/sha256"
)

const (
	// EmptyTreeOID is the Git tree object hash that corresponds to an empty tree (directory)
	EmptyTreeOID = sha256.EmptyTreeOID
)

var initRepoExtraArgs = []string{"--object-format=sha256"}

// NewObjectIDFromHex constructs a new ObjectID from the given hex
// representation of the object ID. Returns ErrInvalidObjectID if the given
// OID is not valid.
func NewObjectIDFromHex(hex string) (git.ObjectID, error) {
	return sha256.NewObjectIDFromHex(hex)
}
