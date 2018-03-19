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
	var entries []*pb.TreeEntry
	var modeBytes, filename []byte
	var err error

	// Non-existing tree, return empty entry list
	if len(treeInfo.Oid) == 0 {
		return nil, nil
	}

	treeData := &bytes.Buffer{}
	if _, err := io.CopyN(treeData, stdout, treeInfo.Size); err != nil {
		return nil, fmt.Errorf("read tree data: %v", err)
	}

	// Extra byte for the linefeed at the end
	if _, err := io.CopyN(ioutil.Discard, stdout, 1); err != nil {
		return nil, fmt.Errorf("stdout discard: %v", err)
	}

	oidBytes := make([]byte, oidSize)
	for treeData.Len() > 0 {
		modeBytes, err = treeData.ReadBytes(' ')
		if err != nil || len(modeBytes) <= 1 {
			return nil, fmt.Errorf("read entry mode: %v", err)
		}
		modeBytes = modeBytes[:len(modeBytes)-1]

		filename, err = treeData.ReadBytes('\x00')
		if err != nil || len(filename) <= 1 {
			return nil, fmt.Errorf("read entry path: %v", err)
		}
		filename = filename[:len(filename)-1]

		if n, _ := treeData.Read(oidBytes); n != oidSize {
			return nil, fmt.Errorf("read entry oid: short read: %d bytes", n)
		}

		treeEntry, err := newTreeEntry(commitOid, rootOid, rootPath, filename, oidBytes, modeBytes)
		if err != nil {
			return nil, fmt.Errorf("new entry info: %v", err)
		}

		entries = append(entries, treeEntry)
	}

	return entries, nil
}

func treeEntries(revision, path string, stdin io.Writer, stdout *bufio.Reader, includeRootOid bool, rootOid string, recursive bool) ([]*pb.TreeEntry, error) {
	if path == "." {
		path = ""
	}

	var entries []*pb.TreeEntry

	if path == "" || includeRootOid {
		// We always need to process the root path to get the rootTreeInfo.Oid
		rootTreeInfo, err := getTreeInfo(revision, "", stdin, stdout)
		if err != nil {
			return nil, err
		}

		rootOid = rootTreeInfo.Oid

		entries, err = extractEntryInfoFromTreeData(stdout, revision, rootOid, "", rootTreeInfo)
		if err != nil {
			return nil, err
		}
	}

	if path != "" {
		treeEntryInfo, err := getTreeInfo(revision, path, stdin, stdout)
		if err != nil {
			return nil, err
		}
		if treeEntryInfo.Type != "tree" {
			return nil, nil
		}

		entries, err = extractEntryInfoFromTreeData(stdout, revision, rootOid, path, treeEntryInfo)
		if err != nil {
			return nil, err
		}
	}

	if !recursive {
		return entries, nil
	}

	var orderdEntries []*pb.TreeEntry
	for _, entry := range entries {
		orderdEntries = append(orderdEntries, entry)

		if entry.Type == pb.TreeEntry_TREE {
			subentries, err := treeEntries(revision, string(entry.Path), stdin, stdout, true, rootOid, true)
			if err != nil {
				return nil, err
			}

			orderdEntries = append(orderdEntries, subentries...)
		}
	}

	return orderdEntries, nil
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
