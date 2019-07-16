package packfile

import "fmt"

// ObjectType is used to label index entries as commits, trees, blobs or tags.
type ObjectType byte

const (
	// TUnknown is a sentinel indicating an object has not been labeled yet
	TUnknown ObjectType = iota
	// TBlob means Git blob
	TBlob
	// TCommit means Git commit
	TCommit
	// TTree means Git tree
	TTree
	// TTag means Git tag
	TTag
)

// Object represents a Git packfile index entry, optionally decorated with its object type.
type Object struct {
	OID    string
	Type   ObjectType
	Offset uint64
}

func (o Object) String() string {
	t := "unknown"
	switch o.Type {
	case TBlob:
		t = "blob"
	case TCommit:
		t = "commit"
	case TTree:
		t = "tree"
	case TTag:
		t = "tag"
	}

	return fmt.Sprintf("%s %s\t%d", o.OID, t, o.Offset)
}
