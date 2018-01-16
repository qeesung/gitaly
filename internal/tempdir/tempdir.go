package tempdir

import (
	"io/ioutil"
	"os"
	"path"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

const (
	// We need to be careful that this path does not clash with any
	// directory name that could be provided by a user. The '+' character is
	// not allowed in GitLab namespaces or repositories.
	tmpRoot = "+gitaly/tmp"
)

// New returns the path of a new temporary directory for use with the
// repository. The caller must os.RemoveAll the directory when done.
func New(repo *pb.Repository) (string, error) {
	storageDir, err := helper.GetStorageByName(repo.StorageName)
	if err != nil {
		return "", err
	}

	root := path.Join(storageDir, tmpRoot)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}

	return ioutil.TempDir(root, "repo")
}
