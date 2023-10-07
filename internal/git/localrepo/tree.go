package localrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// Entries holds every ls-tree Entry
type Entries []TreeEntry

func (e Entries) Len() int {
	return len(e)
}

func (e Entries) Swap(i, j int) {
	e[i], e[j] = e[j], e[i]
}

// Less sorts entries by type in the order [*tree *blobs *submodules]
func (e Entries) Less(i, j int) bool {
	return e[i].Type < e[j].Type
}

// TreeEntriesByPath allows a slice of *TreeEntry to be sorted by Path
type TreeEntriesByPath []*TreeEntry

func (b TreeEntriesByPath) Len() int {
	return len(b)
}

func (b TreeEntriesByPath) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}

func (b TreeEntriesByPath) Less(i, j int) bool {
	iPath, jPath := b[i].Path, b[j].Path

	// git has an edge case for subtrees where they are always appended with
	// a '/'. See https://github.com/git/git/blob/v2.40.0/read-cache.c#L491
	if b[i].Type == Tree {
		iPath += "/"
	}

	if b[j].Type == Tree {
		jPath += "/"
	}

	return iPath < jPath
}

// TreeEntry represents an entry of a git tree object.
type TreeEntry struct {
	// OID is the object ID the tree entry refers to.
	OID git.ObjectID
	// Mode is the file mode of the tree entry.
	Mode string
	// Path is the full path of the tree entry.
	Path string
	// Type is the type of the tree entry.
	Type ObjectType
	// Entries is a slice of this tree's entries.
	Entries []*TreeEntry
}

// IsBlob returns whether or not the TreeEntry is a blob.
func (t *TreeEntry) IsBlob() bool {
	return t.Type == Blob
}

var (
	// ErrTreeNotExist indicates that the requested tree does not exist, either because the revision
	// is invalid or because the path is not valid.
	ErrTreeNotExist = errors.New("invalid object name")
	// ErrNotTreeish indicates that the requested revision or path does not resolve to a tree
	// object.
	ErrNotTreeish = errors.New("not treeish")
	// ErrEntryNotFound indicates an entry could not be found.
	ErrEntryNotFound = errors.New("entry not found")
	// ErrEntryExists indicates an entry already exists.
	ErrEntryExists = errors.New("entry already exists")
	// ErrPathTraversal indicates a path contains a traversal.
	ErrPathTraversal = errors.New("path contains traversal")
	// ErrAbsolutePath indicates the path is an absolute path
	ErrAbsolutePath = errors.New("path is absolute")
	// ErrEmptyPath indicates the path is an absolute path
	ErrEmptyPath = errors.New("path is empty")
	// ErrDisallowedPath indicates the path is invalid
	ErrDisallowedPath = errors.New("disallowed path")
)

func validateFileCreationPath(path string) (string, error) {
	path = filepath.Clean(path)

	if filepath.IsAbs(path) {
		return "", ErrAbsolutePath
	}

	if strings.HasPrefix(path, "..") {
		return "", ErrPathTraversal
	}

	if path == "." {
		return "", ErrEmptyPath
	}

	if before, _, _ := strings.Cut(path, "/"); before == ".git" {
		return "", ErrDisallowedPath
	}

	return path, nil
}

type addTreeEntryConfig struct {
	overwriteFile bool
	overwriteDir  bool
}

// AddTreeEntryOption is a function that sets options on the addTreeEntryConfig
// struct.
type AddTreeEntryOption func(*addTreeEntryConfig)

// WithOverwriteFile allows Add to overwrite a file
func WithOverwriteFile() AddTreeEntryOption {
	return func(a *addTreeEntryConfig) {
		a.overwriteFile = true
	}
}

// WithOverwriteDirectory allows Add to overwrite a directory
func WithOverwriteDirectory() AddTreeEntryOption {
	return func(a *addTreeEntryConfig) {
		a.overwriteDir = true
	}
}

// Add takes an entry and adds it to an existing TreeEntry
// based on path.
// It works by walking down the TreeEntry's Entries, layer by layer, based on
// path. If it reaches the limit when walking down the tree, that means the
// rest of the directories path will need to be created.
// If we're able to walk all the way down the tree based on path, then we
// either add the new entry to the last subtree's entries if it doesn't yet
// exist, or optionally overwrite the entry if it already exists.
func (t *TreeEntry) Add(
	path string,
	newEntry TreeEntry,
	options ...AddTreeEntryOption,
) error {
	path, err := validateFileCreationPath(path)
	if err != nil {
		return err
	}

	var cfg addTreeEntryConfig

	for _, option := range options {
		option(&cfg)
	}

	var firstComponent string
	var parentDirs string

	secondComponent := path

	// currentTree keeps track of the current tree we are examining.
	currentTree := t

Loop:
	for {
		// loop through each layer of the tree based on the directory
		// structure specified by path.
		// firstComponent is the current directory name, and secondComponent is the rest
		// of the sub directories we have yet to walk down into.
		firstComponent, secondComponent, _ = strings.Cut(secondComponent, "/")

		// look through the current tree's entries.
		for i, entry := range currentTree.Entries {
			// If the entry's Path does not match firstComponent, we don't
			// want to do anything with it.
			if entry.Path != firstComponent {
				continue
			}

			// If the entry's Path does match firstComponent, then either
			// we found the next directory to walk into, or we've
			// found the entry we want to replace.

			// We found an entry in the tree that matches the entry
			// we want to add. Replace the entry or throw an error,
			// depending on the options passed in via options
			if filepath.Join(parentDirs, entry.Path) == path {
				if (entry.Type == Tree && !cfg.overwriteDir) ||
					(entry.Type == Blob && !cfg.overwriteFile) {
					return ErrEntryExists
				}

				currentTree.OID = ""
				currentTree.Entries[i] = &newEntry

				return nil
			}

			// We found the next directory to walk into.
			currentTree.OID = ""
			currentTree = entry
			parentDirs = filepath.Join(parentDirs, entry.Path)
			continue Loop
		}

		// If we get here, that means we didn't find any directories to
		// recurse into, which means we need to create a brand new
		// tree
		if firstComponent == filepath.Base(path) {
			currentTree.OID = ""
			currentTree.Entries = append(
				currentTree.Entries,
				&newEntry,
			)

			return nil
		}

		currentTree.Entries = append(currentTree.Entries, &TreeEntry{
			Mode: "040000",
			Type: Tree,
			Path: firstComponent,
		})

		currentTree.OID = ""
		currentTree = currentTree.Entries[len(currentTree.Entries)-1]

		if secondComponent == "" {
			return nil
		}
	}
}

// recurse is a common function to recurse down into a TreeEntry based on path,
// and take some action once we've found the entry in question.
// entryFn is called on the entry specified by path.
// treeFn is called on each tree visited during the walk.
func (t *TreeEntry) recurse(
	path string,
	entryFn func(currentTree, entry *TreeEntry, i int) error,
	treeFn func(entry *TreeEntry) error,
) error {
	var firstComponent string
	var parentDirs string
	secondComponent := path

	currentTree := t
	for {
		// Loop through each layer of the tree based on the directory
		// structure specified by path.
		// firstComponent is the current directory name, and secondComponent is the rest
		// of the sub directories we have yet to walk down into.
		firstComponent, secondComponent, _ = strings.Cut(secondComponent, "/")

		// Look through the current tree's entries.
		for i, entry := range currentTree.Entries {
			if firstComponent != entry.Path {
				continue
			}

			if filepath.Join(parentDirs, entry.Path) == path {
				currentTree.OID = ""

				// Once we find the entry in question, apply the
				// function to modify the current tree and/or
				// entry.
				return entryFn(currentTree, entry, i)
			}

			if err := treeFn(currentTree); err != nil {
				return err
			}
			currentTree = entry
			parentDirs = filepath.Join(parentDirs, entry.Path)
		}

		if secondComponent == "" {
			return ErrEntryNotFound
		}
	}
}

// Delete deletes the entry of a current tree based on the path. The parent
// entry is deleted if the entry is the last child of the parent, this
// propagates up to the root node, but the root node is never deleted.
func (t *TreeEntry) Delete(
	path string,
) error {
	path, err := validateFileCreationPath(path)
	if err != nil {
		return err
	}

	entriesRemaining := 0

	if err := t.recurse(
		path,
		func(currentTree, entry *TreeEntry, i int) error {
			currentTree.Entries = append(
				currentTree.Entries[:i],
				currentTree.Entries[i+1:]...)

			entriesRemaining = len(currentTree.Entries)

			return nil
		},
		func(entry *TreeEntry) error {
			entry.OID = ""
			return nil
		},
	); err != nil {
		return err
	}

	// We check if the child was last child entry of the tree, if so, we delete the
	// parent entry too.
	//
	// We check for "." directory because filepath.Dir resolves the root dir to ".".
	if dir := filepath.Dir(path); entriesRemaining == 0 && dir != "." {
		return t.Delete(dir)
	}

	return nil
}

// Get gets an entry of a current tree based on the path.
func (t *TreeEntry) Get(
	path string,
) (*TreeEntry, error) {
	if path == "" || path == "." {
		return t, nil
	}

	var result *TreeEntry

	if err := t.recurse(path, func(currentTree, entry *TreeEntry, i int) error {
		result = entry
		return nil
	},
		func(entry *TreeEntry) error {
			return nil
		},
	); err != nil {
		return nil, err
	}

	return result, nil
}

// Modify modifies an existing TreeEntry based on a path and a function to
// modify an entry.
func (t *TreeEntry) Modify(
	path string,
	modifyEntry func(*TreeEntry) error,
) error {
	path, err := validateFileCreationPath(path)
	if err != nil {
		return err
	}

	return t.recurse(path, func(currentTree, entry *TreeEntry, _ int) error {
		return modifyEntry(entry)
	},
		func(entry *TreeEntry) error {
			entry.OID = ""
			return nil
		},
	)
}

// listEntries lists tree entries for the given treeish.
func (repo *Repo) listEntries(
	ctx context.Context,
	treeish git.Revision,
	relativePath string,
	recursive bool,
) ([]*TreeEntry, error) {
	flags := []git.Option{git.Flag{Name: "-z"}}
	if recursive {
		flags = append(flags,
			git.Flag{Name: "-r"},
			// By default, when -r is passed, tree entries will not
			// be shown. -t will cause tree entries to be shown as
			// well even when -r is passed.
			git.Flag{Name: "-t"},
		)
	}

	if relativePath == "." {
		relativePath = ""
	}

	var stderr bytes.Buffer
	cmd, err := repo.Exec(ctx, git.Command{
		Name:  "ls-tree",
		Args:  []string{fmt.Sprintf("%s:%s", treeish, relativePath)},
		Flags: flags,
	}, git.WithStderr(&stderr), git.WithSetupStdout())
	if err != nil {
		return nil, fmt.Errorf("spawning git-ls-tree: %w", err)
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting object hash: %w", err)
	}

	parser := NewParser(cmd, objectHash)
	var entries []*TreeEntry
	for {
		entry, err := parser.NextEntry()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, fmt.Errorf("parsing tree entry: %w", err)
		}

		entries = append(entries, entry)
	}

	if err := cmd.Wait(); err != nil {
		errorMessage := stderr.String()
		if strings.HasPrefix(errorMessage, "fatal: not a tree object") {
			return nil, ErrNotTreeish
		} else if strings.HasPrefix(errorMessage, "fatal: Not a valid object name") {
			return nil, ErrTreeNotExist
		}

		return nil, structerr.New("waiting for git-ls-tree: %w", err).WithMetadata("stderr", errorMessage)
	}

	return entries, nil
}

// depthByPath must be called only on cleaned paths
func depthByPath(path string) int {
	return strings.Count(path, "/") + 1
}

// treeStack is a stack data structure used by walk() to do a breadth-first walk
// of the outputof ls-tree -r.
type treeStack []*TreeEntry

func (t treeStack) peek() *TreeEntry {
	if len(t) == 0 {
		return nil
	}

	return t[len(t)-1]
}

func (t *treeStack) push(e *TreeEntry) {
	*t = append(*t, e)
}

func (t *treeStack) pop() *TreeEntry {
	e := t.peek()
	if e == nil {
		return nil
	}

	*t = (*t)[:len(*t)-1]

	return e
}

// populate scans through the output of ls-tree -r, and constructs a TreeEntry
// object.
func (t *TreeEntry) populate(
	ctx context.Context,
	repo *Repo,
) error {
	if t.OID == "" {
		return errors.New("oid is empty")
	}

	t.Entries = nil

	entries, err := repo.listEntries(
		ctx,
		git.Revision(t.OID),
		"",
		true,
	)
	if err != nil {
		return err
	}

	stack := treeStack{t}

	// The outpout of ls-tree -r is the following:
	// a1
	// dir1/file2
	// dir2/file3
	// f2
	// f3
	// Whenever we see a tree, push it onto the stack since we will need to
	// start adding to that tree's entries.
	// When we encounter an entry that has a lower depth than the previous
	// entry, we know that we need to pop the tree entry off to get back to the
	// parent tree.
	for _, entry := range entries {
		if levelsToPop := len(stack) - depthByPath(entry.Path); levelsToPop > 0 {
			for i := 0; i < levelsToPop; i++ {
				stack.pop()
			}
		}

		entry.Path = filepath.Base(entry.Path)
		stack.peek().Entries = append(
			stack.peek().Entries,
			entry,
		)

		if entry.Type == Tree {
			stack.push(entry)
		}
	}

	if err != nil {
		return fmt.Errorf("listing entries: %w", err)
	}

	return nil
}

func (t *TreeEntry) walk(dirPath string, callback func(path string, entry *TreeEntry) error) error {
	nextDirPath := filepath.Join(dirPath, t.Path)
	if err := callback(dirPath, t); err != nil {
		return err
	}

	for _, entry := range t.Entries {
		if err := entry.walk(nextDirPath, callback); err != nil {
			return err
		}
	}

	return nil
}

// Walk will walk the whole tree structure and call callback on every entry of
// the tree in a depth-first like fashion.
func (t *TreeEntry) Walk(callback func(path string, entry *TreeEntry) error) error {
	for _, e := range t.Entries {
		if err := e.walk(t.Path, callback); err != nil {
			return err
		}
	}

	return nil
}

// Write does a depth first walk, writing new trees when the OID of the
// TreeEntry is empty.
func (t *TreeEntry) Write(
	ctx context.Context,
	repo *Repo,
) error {
	var err error

	if t.OID != "" {
		return nil
	}

	for _, e := range t.Entries {
		if e.Type == Tree && e.OID == "" {
			if err := e.Write(ctx, repo); err != nil {
				return err
			}
		}
	}

	treeOID, err := repo.writeEntries(ctx, t.Entries)
	if err != nil {
		return fmt.Errorf("writing tree: %w", err)
	}

	t.OID = treeOID
	return nil
}

// writeTreeEntries writes a new tree object from a slice of entries. This function does not verify whether OIDs
// referred to by tree entries actually exist in the repository.
func (repo *Repo) writeEntries(ctx context.Context, entries []*TreeEntry) (git.ObjectID, error) {
	var tree bytes.Buffer

	sort.Stable(TreeEntriesByPath(entries))

	for _, entry := range entries {
		mode := strings.TrimPrefix(entry.Mode, "0")
		formattedEntry := fmt.Sprintf("%s %s\000", mode, entry.Path)

		oidBytes, err := entry.OID.Bytes()
		if err != nil {
			return "", err
		}

		if _, err := tree.WriteString(formattedEntry); err != nil {
			return "", err
		}

		if _, err := tree.Write(oidBytes); err != nil {
			return "", err
		}
	}

	options := []git.Option{
		git.ValueFlag{Name: "-t", Value: "tree"},
		git.Flag{Name: "-w"},
		git.Flag{Name: "--stdin"},
	}

	var stdout, stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name:  "hash-object",
			Flags: options,
		},
		git.WithStdout(&stdout),
		git.WithStderr(&stderr),
		git.WithStdin(&tree),
	); err != nil {
		return "", structerr.New("%w", err).WithMetadata("stderr", stderr.String())
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return "", fmt.Errorf("detecting object hash: %w", err)
	}

	treeOID, err := objectHash.FromHex(text.ChompBytes(stdout.Bytes()))
	if err != nil {
		return "", err
	}

	return treeOID, nil
}

// readTreeConfig is configuration that can be passed to ReadTree.
type readTreeConfig struct {
	recursive bool
	// relativePath is the relative path at which listing of entries should be started.
	relativePath string
}

// ReadTreeOption is an option that modifies the behavior of ReadTree()
type ReadTreeOption func(c *readTreeConfig)

// WithRecursive returns all entries recursively, but "flattened" in the sense
// that all subtrees and their entries get returned as Entries, each entry with
// its full path.
func WithRecursive() ReadTreeOption {
	return func(c *readTreeConfig) {
		c.recursive = true
	}
}

// WithRelativePath will cause ReadTree to return a tree at the relative path.
func WithRelativePath(relativePath string) ReadTreeOption {
	return func(c *readTreeConfig) {
		c.relativePath = relativePath
	}
}

// ReadTree gets a tree object with all of the direct children populated.
// Walk can be called to populate every level of the tree.
func (repo *Repo) ReadTree(ctx context.Context, treeish git.Revision, options ...ReadTreeOption) (*TreeEntry, error) {
	var c readTreeConfig

	for _, opt := range options {
		opt(&c)
	}

	if c.relativePath == "." {
		c.relativePath = ""
	}

	treeOID, err := repo.ResolveRevision(ctx, git.Revision(string(treeish)+":"+c.relativePath))
	if err != nil {
		if errors.Is(err, git.ErrReferenceNotFound) {
			return "", structerr.NewInvalidArgument("submodule: %s", legacyErrPrefixInvalidSubmodulePath)
		}
		return nil, fmt.Errorf("getting revision: %w", err)
	}

	rootEntry := &TreeEntry{
		OID:  treeOID,
		Type: Tree,
		Mode: "040000",
	}

	if c.recursive {
		if err := rootEntry.populate(ctx, repo); err != nil {
			return nil, err
		}
	} else {
		if rootEntry.Entries, err = repo.listEntries(
			ctx,
			treeish,
			c.relativePath,
			c.recursive,
		); err != nil {
			return nil, fmt.Errorf("listEntries: %w", err)
		}
	}

	return rootEntry, nil
}
