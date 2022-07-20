package git

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

var (
	// ObjectHashSHA1 is the implementation of an object ID via SHA1.
	ObjectHashSHA1 = ObjectHash{
		regexp:       regexp.MustCompile(`\A[0-9a-f]{40}\z`),
		EmptyTreeOID: ObjectID("4b825dc642cb6eb9a060e54bf8d69288fbee4904"),
		ZeroOID:      ObjectID("0000000000000000000000000000000000000000"),
	}

	// ObjectHashSHA256 is the implementation of an object ID via SHA256.
	ObjectHashSHA256 = ObjectHash{
		regexp:       regexp.MustCompile(`\A[0-9a-f]{64}\z`),
		EmptyTreeOID: ObjectID("6ef19b41225c5369f1c104d45d8d85efa9b057b53b14b4b9b939dd74decc5321"),
		ZeroOID:      ObjectID("0000000000000000000000000000000000000000000000000000000000000000"),
	}

	// ErrInvalidObjectID is returned in case an object ID's string
	// representation is not a valid one.
	ErrInvalidObjectID = errors.New("invalid object ID")
)

// ObjectHash is a hash-function specific implementation of an object ID.
type ObjectHash struct {
	regexp *regexp.Regexp
	// EmptyTreeOID is the object ID of the tree object that has no directory entries.
	EmptyTreeOID ObjectID
	// ZeroOID is the special value that Git uses to signal a ref or object does not exist
	ZeroOID ObjectID
}

// DetectObjectHash detects the object-hash used by the given repository.
func DetectObjectHash(ctx context.Context, repoExecutor RepositoryExecutor) (ObjectHash, error) {
	var stdout bytes.Buffer

	if err := repoExecutor.ExecAndWait(ctx, SubCmd{
		Name: "config",
		Args: []string{"extensions.objectFormat"},
	}, WithStdout(&stdout)); err != nil {
		if status, ok := command.ExitStatus(err); ok && status == 1 {
			return ObjectHashSHA1, nil
		}

		return ObjectHash{}, fmt.Errorf("reading object format: %w", err)
	}

	objectFormat := text.ChompBytes(stdout.Bytes())
	switch objectFormat {
	case "sha1":
		return ObjectHashSHA1, nil
	case "sha256":
		return ObjectHashSHA256, nil
	default:
		return ObjectHash{}, fmt.Errorf("unknown object format: %q", objectFormat)
	}
}

// FromHex constructs a new ObjectID from the given hex representation of the object ID. Returns
// ErrInvalidObjectID if the given object ID is not valid.
func (h ObjectHash) FromHex(hex string) (ObjectID, error) {
	if err := h.ValidateHex(hex); err != nil {
		return "", err
	}

	return ObjectID(hex), nil
}

// ValidateHex checks if `hex` is a syntactically correct object ID for the given hash. Abbreviated
// object IDs are not deemed to be valid. Returns an `ErrInvalidObjectID` if the `hex` is not valid.
func (h ObjectHash) ValidateHex(hex string) error {
	if h.regexp.MatchString(hex) {
		return nil
	}

	return fmt.Errorf("%w: %q", ErrInvalidObjectID, hex)
}

// IsZeroOID checks whether the given object ID is the all-zeroes object ID for the given hash.
func (h ObjectHash) IsZeroOID(oid ObjectID) bool {
	return string(oid) == string(h.ZeroOID)
}

// ObjectID represents an object ID.
type ObjectID string

// String returns the hex representation of the ObjectID.
func (oid ObjectID) String() string {
	return string(oid)
}

// Bytes returns the byte representation of the ObjectID.
func (oid ObjectID) Bytes() ([]byte, error) {
	decoded, err := hex.DecodeString(string(oid))
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

// Revision returns a revision of the ObjectID. This directly returns the hex
// representation as every object ID is a valid revision.
func (oid ObjectID) Revision() Revision {
	return Revision(oid.String())
}
