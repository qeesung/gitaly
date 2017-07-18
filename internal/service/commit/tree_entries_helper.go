package commit

import (
	"bufio"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
)

func getTreeInfo(revision, path string, stdin io.Writer, stdout *bufio.Reader) (*catfile.ObjectInfo, error) {
	if _, err := fmt.Fprintf(stdin, "%s^{tree}:%s\n", revision, path); err != nil {
		return nil, grpc.Errorf(codes.Internal, "TreeEntry: stdin write: %v", err)
	}

	treeInfo, err := catfile.ParseObjectInfo(stdout)
	if err != nil {
		return nil, grpc.Errorf(codes.Internal, "TreeEntry: %v", err)
	}
	return treeInfo, nil
}

func extractEntryInfoFromTreeData(stdout *bufio.Reader, commitOid, rootOid, rootPath string, treeInfo *catfile.ObjectInfo) ([]*pb.TreeEntry, error) {
	var entries []*pb.TreeEntry
	var modeBytes, filename []byte
	var err error

	// Non-existing tree, return empty entry list
	if len(treeInfo.Oid) == 0 {
		return entries, nil
	}

	oidBytes := make([]byte, 20)
	bytesLeft := treeInfo.Size

	for bytesLeft > 0 {
		modeBytes, err = stdout.ReadBytes(' ')
		if err != nil || len(modeBytes) <= 1 {
			return nil, fmt.Errorf("read entry mode: %v", err)
		}
		bytesLeft -= int64(len(modeBytes))
		modeBytes = modeBytes[:len(modeBytes)-1]

		filename, err = stdout.ReadBytes('\x00')
		if err != nil || len(filename) <= 1 {
			return nil, fmt.Errorf("read entry path: %v", err)
		}
		bytesLeft -= int64(len(filename))
		filename = filename[:len(filename)-1]

		// bufio.Reader.Read isn't guaranteed to read len(p) since bytes
		// are taken from at most one Read on the underlying Reader.
		// We call Peek to make sure we have enough bytes buffered to read into oidBytes.
		if _, err := stdout.Peek(len(oidBytes)); err != nil {
			return nil, fmt.Errorf("peek entry oid: %v", err)
		}
		if n, err := stdout.Read(oidBytes); n != 20 || err != nil {
			return nil, fmt.Errorf("read entry oid: %v", err)
		}

		bytesLeft -= int64(len(oidBytes))

		treeEntry, err := newTreeEntry(commitOid, rootOid, rootPath, filename, oidBytes, modeBytes)
		if err != nil {
			return nil, fmt.Errorf("new entry info: %v", err)
		}

		entries = append(entries, treeEntry)
	}

	// Extra byte for a linefeed at the end
	if _, err := stdout.Discard(int(bytesLeft + 1)); err != nil {
		return nil, fmt.Errorf("stdout discard: %v", err)
	}

	return entries, nil
}

func treeEntries(revision, path string, stdin io.Writer, stdout *bufio.Reader) ([]*pb.TreeEntry, error) {
	if path == "." {
		path = ""
	}

	// We always need to process the root path to get the rootTreeInfo.Oid
	rootTreeInfo, err := getTreeInfo(revision, "", stdin, stdout)
	if err != nil {
		return nil, err
	}
	entries, err := extractEntryInfoFromTreeData(stdout, revision, rootTreeInfo.Oid, "", rootTreeInfo)
	if err != nil {
		return nil, err
	}

	// If we were asked for the root path, good luck! We're done
	if path == "" {
		return entries, nil
	}

	treeInfo, err := getTreeInfo(revision, path, stdin, stdout)
	if err != nil {
		return nil, err
	}
	return extractEntryInfoFromTreeData(stdout, revision, rootTreeInfo.Oid, path, treeInfo)
}
