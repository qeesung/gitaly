package commit

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	pathPkg "path"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
)

// if getTreeInfo returns a struct treeInfo with treeInfo.Oid != "", then
// the caller must read (treeInfo.Size + 1) bytes from stdout afterwards
func getTreeInfo(revision, path string, stdin io.Writer, stdout *bufio.Reader) (*catfile.ObjectInfo, error) {
	if _, err := fmt.Fprintf(stdin, "%s^{tree}:%s\n", revision, path); err != nil {
		return nil, status.Errorf(codes.Internal, "TreeEntry: stdin write: %v", err)
	}

	treeInfo, err := catfile.ParseObjectInfo(stdout)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "TreeEntry: %v", err)
	}
	return treeInfo, nil
}

const oidSize = 20

func extractEntryInfoFromTreeData(stdout *bufio.Reader, commitOid, rootOid, rootPath string, treeInfo *catfile.ObjectInfo) ([]*pb.TreeEntry, error) {
	if len(treeInfo.Oid) == 0 {
		return nil, fmt.Errorf("empty tree oid")
	}

	treeData := &bytes.Buffer{}
	if _, err := io.CopyN(treeData, stdout, treeInfo.Size); err != nil {
		return nil, fmt.Errorf("read tree data: %v", err)
	}

	if _, err := io.CopyN(ioutil.Discard, stdout, 1); err != nil {
		return nil, fmt.Errorf("discard cat-file linefeed: %v", err)
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

func treeEntries(revision, path string, stdin io.Writer, stdout *bufio.Reader, lookupRootOid bool, rootOid string, recursive bool) ([]*pb.TreeEntry, error) {
	if path == "." {
		path = ""
	}

	if lookupRootOid {
		rootTreeInfo, err := getTreeInfo(revision, "", stdin, stdout)
		if err != nil {
			return nil, err
		}

		if len(rootTreeInfo.Oid) == 0 {
			// 'revision' does not point to a commit
			return nil, nil
		}

		// Ideally we'd use a 'git cat-file --batch-check' process so we don't
		// have to discard this data. But tree objects are small so it is not a
		// problem.
		if _, err := io.CopyN(ioutil.Discard, stdout, rootTreeInfo.Size+1); err != nil {
			return nil, err
		}
		rootOid = rootTreeInfo.Oid
	}

	treeEntryInfo, err := getTreeInfo(revision, path, stdin, stdout)
	if err != nil {
		return nil, err
	}

	if treeEntryInfo.Type != "tree" {
		return nil, nil
	}

	entries, err := extractEntryInfoFromTreeData(stdout, revision, rootOid, path, treeEntryInfo)
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
			subentries, err := treeEntries(revision, string(entry.Path), stdin, stdout, false, rootOid, true)
			if err != nil {
				return nil, err
			}

			orderedEntries = append(orderedEntries, subentries...)
		}
	}

	return orderedEntries, nil
}

// TreeEntryForRevisionAndPath returns a TreeEntry struct for the object present at the revision/path pair.
func TreeEntryForRevisionAndPath(revision, path string, stdin io.Writer, stdout *bufio.Reader) (*pb.TreeEntry, error) {
	entries, err := treeEntries(revision, pathPkg.Dir(path), stdin, stdout, false, "", false)
	if err != nil {
		return nil, err
	}

	var treeEntry *pb.TreeEntry

	for _, entry := range entries {
		if string(entry.Path) == path {
			treeEntry = entry
			break
		}
	}

	return treeEntry, nil
}
