package wal

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// referenceTree is a helper type that is used to build a tree representation
// of references.
type referenceTree struct {
	component string
	children  map[string]*referenceTree
}

// newReferenceTree returns a new reference tree. The returned reference
// tree contains already the 'refs' directory.
func newReferenceTree() *referenceTree {
	return &referenceTree{
		component: "refs",
		children:  map[string]*referenceTree{},
	}
}

// Contains returns whether a given path exists in the reference tree.
func (rt *referenceTree) Contains(path string) bool {
	return rt.find(path) != nil
}

// find returns a reference tree node at the given path.
func (rt *referenceTree) find(path string) *referenceTree {
	node := rt
	components := strings.Split(path, "/")
	for i := 0; i < len(components); i++ {
		if node == nil || node.component != components[i] {
			return nil
		}

		if i < len(components)-1 {
			node = node.children[components[i+1]]
		}
	}

	return node
}

// Insert takes in a fully-qualified reference path and inserts it into the
// reference tree. The path is split up into components and inserted into
// the tree hierarchy at correct locations.
func (rt *referenceTree) Insert(path string) error {
	remainder, foundSeparator := strings.CutPrefix(path, "refs/")
	if !foundSeparator {
		return errors.New("expected a fully qualified reference")
	}

	return rt.insert(remainder)
}

func (rt *referenceTree) insert(path string) error {
	if rt.children == nil {
		return errors.New("directory-file conflict")
	}

	// Split the path into its components. For example, 'heads/subdir/branch-1'
	// is split into 'heads' and 'subdir/branch-1'
	component, remainder, foundSeparator := strings.Cut(path, "/")

	// See if the child already exists.
	childNode := rt.children[component]
	if childNode == nil {
		// The child did not exist yet. Create it.
		childNode = &referenceTree{component: component}
		rt.children[component] = childNode
		if !foundSeparator {
			// This was the last element of the path so this is a file.
			return nil
		}

		// There were further components in the path so this
		// child is a directory.
		childNode.children = map[string]*referenceTree{}
	} else if !foundSeparator {
		// Attempted to insert a path that already existed.
		return errors.New("path already exists")
	}

	// Recurse down the hierarchy and insert the remainder in the child
	// directory.
	if err := childNode.insert(remainder); err != nil {
		return err
	}

	return nil
}

// walkCallback is a callback function that is invoked by the Walk* methods when
// walking the reference tree. Path is the path in the refs directory relative to
// the repository root, so for example 'refs' or 'refs/heads/main'. isDir indicates
// whether the path is a directory or not.
type walkCallback func(path string, isDir bool) error

// WalkPreOrder walks the reference tree invoking the callback for each
// entry it finds. WalkPreOrder invokes the callback on directories
// before invoking it on the children of the directories.
func (rt *referenceTree) WalkPreOrder(callback walkCallback) error {
	return rt.walk("refs", callback, true)
}

// WalkPostOrder walks the reference tree invoking the callback for each
// entry it finds. WalkPostOrder invokes the callback on directories
// after invoking it on the children of the directories.
func (rt *referenceTree) WalkPostOrder(callback walkCallback) error {
	return rt.walk("refs", callback, false)
}

func (rt *referenceTree) walk(path string, callback walkCallback, preOrder bool) error {
	isDir := rt.children != nil

	if preOrder {
		if err := callback(path, isDir); err != nil {
			return fmt.Errorf("pre-order callback: %w", err)
		}
	}

	sortedChildren := make([]*referenceTree, 0, len(rt.children))
	for _, child := range rt.children {
		sortedChildren = append(sortedChildren, child)
	}

	// Walk the children in lexicographical order to produce deterministic ordering.
	sort.Slice(sortedChildren, func(i, j int) bool {
		return sortedChildren[i].component < sortedChildren[j].component
	})

	for _, child := range sortedChildren {
		if err := child.walk(filepath.Join(path, child.component), callback, preOrder); err != nil {
			return fmt.Errorf("walk: %w", err)
		}
	}

	if !preOrder {
		if err := callback(path, isDir); err != nil {
			return fmt.Errorf("post-order callback: %w", err)
		}
	}

	return nil
}

// String returns a string representation of the reference tree.
func (rt *referenceTree) String() string {
	sb := &strings.Builder{}

	if err := rt.WalkPreOrder(func(path string, isDir bool) error {
		components := strings.Split(path, "/")

		for i := 0; i < len(components)-1; i++ {
			sb.WriteString(" ")
		}

		sb.WriteString(components[len(components)-1] + "\n")
		return nil
	}); err != nil {
		// This should be never triggered as the callback doesn't
		// return an error.
		panic(err)
	}

	return sb.String()
}
