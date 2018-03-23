package commit

import (
	"bytes"
	"fmt"
	"io"
	pathPkg "path"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
)

const oidSize = 20

func extractEntryInfoFromTreeData(treeData *bytes.Buffer, commitOid, rootOid, rootPath string, treeInfo *catfile.ObjectInfo) ([]*pb.TreeEntry, error) {
	if len(treeInfo.Oid) == 0 {
		return nil, fmt.Errorf("empty tree oid")
	}

	var entries []*pb.TreeEntry
	oidBuf := &bytes.Buffer{}
	for treeData.Len() > 0 {
		modeBytes, err := treeData.ReadBytes(' ')
		if err != nil || len(modeBytes) <= 1 {
			return nil, fmt.Errorf("read entry mode: %v", err)
		}
		modeBytes = modeBytes[:len(modeBytes)-1]

		filename, err := treeData.ReadBytes('\x00')
		if err != nil || len(filename) <= 1 {
			return nil, fmt.Errorf("read entry path: %v", err)
		}
		filename = filename[:len(filename)-1]

		oidBuf.Reset()
		if _, err := io.CopyN(oidBuf, treeData, oidSize); err != nil {
			return nil, fmt.Errorf("read entry oid: %v", err)
		}

		treeEntry, err := newTreeEntry(commitOid, rootOid, rootPath, filename, oidBuf.Bytes(), modeBytes)
		if err != nil {
			return nil, fmt.Errorf("new entry info: %v", err)
		}

		entries = append(entries, treeEntry)
	}

	return entries, nil
}

func treeEntries(c *catfile.Batch, revision, path string, rootOid string, recursive bool) ([]*pb.TreeEntry, error) {
	if path == "." {
		path = ""
	}

	if len(rootOid) == 0 {
		rootTreeInfo, err := c.Info(revision + "^{tree}")
		if err != nil {
			if catfile.IsNotFound(err) {
				return nil, nil
			}

			return nil, err
		}

		rootOid = rootTreeInfo.Oid
	}

	treeEntryInfo, err := c.Info(fmt.Sprintf("%s^{tree}:%s", revision, path))
	if err != nil {
		if catfile.IsNotFound(err) {
			return nil, nil
		}

		return nil, err
	}

	if treeEntryInfo.Type != "tree" {
		return nil, nil
	}

	treeReader, err := c.Tree(treeEntryInfo.Oid)
	if err != nil {
		return nil, err
	}

	treeBytes := &bytes.Buffer{}
	if _, err := treeBytes.ReadFrom(treeReader); err != nil {
		return nil, err
	}

	entries, err := extractEntryInfoFromTreeData(treeBytes, revision, rootOid, path, treeEntryInfo)
	if err != nil {
		return nil, err
	}

	if !recursive {
		return entries, nil
	}

	var orderedEntries []*pb.TreeEntry
	for _, entry := range entries {
		orderedEntries = append(orderedEntries, entry)

		if entry.Type == pb.TreeEntry_TREE {
			subentries, err := treeEntries(c, revision, string(entry.Path), rootOid, true)
			if err != nil {
				return nil, err
			}

			orderedEntries = append(orderedEntries, subentries...)
		}
	}

	return orderedEntries, nil
}

// TreeEntryForRevisionAndPath returns a TreeEntry struct for the object present at the revision/path pair.
func TreeEntryForRevisionAndPath(c *catfile.Batch, revision, path string) (*pb.TreeEntry, error) {
	entries, err := treeEntries(c, revision, pathPkg.Dir(path), "", false)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if string(entry.Path) == path {
			entry.RootOid = "" // Not sure why we do this
			return entry, nil
		}
	}

	return nil, nil
}
