package sha256

import (
	"fmt"
	"regexp"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

const (
	// EmptyTreeOID is the Git tree object sha256 hash that corresponds to an empty tree (directory)
	EmptyTreeOID = git.ObjectID("6ef19b41225c5369f1c104d45d8d85efa9b057b53b14b4b9b939dd74decc5321")
)

var objectIDRegex = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// NewObjectIDFromHex constructs a new ObjectID from the given hex
// representation of the object ID. Returns ErrInvalidObjectID if the given
// OID is not valid.
func NewObjectIDFromHex(hex string) (git.ObjectID, error) {
	if err := ValidateObjectID(hex); err != nil {
		return "", err
	}
	return git.ObjectID(hex), nil
}

// ValidateObjectID checks if id is a syntactically correct object ID. Abbreviated
// object IDs are not deemed to be valid. Returns an ErrInvalidObjectID if the
// id is not valid.
func ValidateObjectID(id string) error {
	if objectIDRegex.MatchString(id) {
		return nil
	}

	return fmt.Errorf("%w: %q", git.ErrInvalidObjectID, id)
}
