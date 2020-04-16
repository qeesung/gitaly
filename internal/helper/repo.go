package helper

import (
	"os"
	"path"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetRepoPath returns the full path of the repository referenced by an
// RPC Repository message. The errors returned are gRPC errors with
// relevant error codes and should be passed back to gRPC without further
// decoration.
func GetRepoPath(repo repository.GitRepo) (string, error) {
	repoPath, err := GetPath(repo)
	if err != nil {
		return "", err
	}

	if repoPath == "" {
		return "", status.Errorf(codes.InvalidArgument, "GetRepoPath: empty repo")
	}

	if IsGitDirectory(repoPath) {
		return repoPath, nil
	}

	return "", status.Errorf(codes.NotFound, "GetRepoPath: not a git repository '%s'", repoPath)
}

// GetPath returns the path of the repo passed as first argument. An error is
// returned when either the storage can't be found or the path includes
// constructs trying to perform directory traversal.
func GetPath(repo repository.GitRepo) (string, error) {
	storagePath, err := GetStorageByName(repo.GetStorageName())
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(storagePath); err != nil {
		return "", status.Errorf(codes.Internal, "GetPath: storage path: %v", err)
	}

	relativePath := repo.GetRelativePath()
	if len(relativePath) == 0 {
		err := status.Errorf(codes.InvalidArgument, "GetPath: relative path missing from %+v", repo)
		return "", err
	}

	if ContainsPathTraversal(relativePath) {
		return "", status.Errorf(codes.InvalidArgument, "GetRepoPath: relative path can't contain directory traversal")
	}

	return path.Join(storagePath, relativePath), nil
}

// GetStorageByName will return the path for the storage, which is fetched by
// its key. An error is return if it cannot be found.
func GetStorageByName(storageName string) (string, error) {
	storagePath, ok := config.Config.StoragePath(storageName)
	if !ok {
		return "", status.Errorf(codes.InvalidArgument, "Storage can not be found by name '%s'", storageName)
	}

	return storagePath, nil
}

// IsGitDirectory checks if the directory passed as first argument looks like
// a valid git directory.
func IsGitDirectory(dir string) bool {
	if dir == "" {
		return false
	}

	for _, element := range []string{"objects", "refs", "HEAD"} {
		if _, err := os.Stat(path.Join(dir, element)); err != nil {
			return false
		}
	}

	// See: https://gitlab.com/gitlab-org/gitaly/issues/1339
	//
	// This is a workaround for Gitaly running on top of an NFS mount. There
	// is a Linux NFS v4.0 client bug where opening the packed-refs file can
	// either result in a stale file handle or stale data. This can happen if
	// git gc runs for a long time while keeping open the packed-refs file.
	// Running stat() on the file causes the kernel to revalidate the cached
	// directory entry. We don't actually care if this file exists.
	os.Stat(path.Join(dir, "packed-refs"))

	return true
}

// GetObjectDirectoryPath returns the full path of the object directory in a
// repository referenced by an RPC Repository message. The errors returned are
// gRPC errors with relevant error codes and should be passed back to gRPC
// without further decoration.
func GetObjectDirectoryPath(repo repository.GitRepo) (string, error) {
	repoPath, err := GetRepoPath(repo)
	if err != nil {
		return "", err
	}

	objectDirectoryPath := repo.GetGitObjectDirectory()
	if objectDirectoryPath == "" {
		return "", status.Errorf(codes.InvalidArgument, "GetObjectDirectoryPath: empty directory")
	}

	if ContainsPathTraversal(objectDirectoryPath) {
		return "", status.Errorf(codes.InvalidArgument, "GetObjectDirectoryPath: relative path can't contain directory traversal")
	}

	fullPath := path.Join(repoPath, objectDirectoryPath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return "", status.Errorf(codes.NotFound, "GetObjectDirectoryPath: does not exist '%s'", fullPath)
	}

	return fullPath, nil
}

// RefreshAllRefPaths attempts to open and close all the directories in
// the loose ref directories. This is a workaround for NFS-mounted
// directories to flush the attribute/directory entry cache to ensure
// the latest loose refs are seen. This takes advantage of close-to-open
// cache consistency (https://linux.die.net/man/5/nfs).
func RefreshAllRefPaths(repoPath string) {
	refPath := filepath.Join(repoPath, "refs")

	file, err := os.Open(refPath)
	if err != nil {
		return
	}
	defer file.Close()

	// We should only expect 5 paths:
	// heads, tags, keep-around, environments, merge-requests
	names, err := file.Readdirnames(10)
	if err != nil {
		return
	}

	for _, n := range names {
		p := filepath.Join(refPath, n)
		OpenAndClosePath(p)
	}
}

// OpenAndClosePath opens and closes a given path. This is a workaround
// for NFS-mounted directories.
func OpenAndClosePath(path string) {
	f, err := os.Open(path)
	if err == nil {
		f.Close()
	}
}
