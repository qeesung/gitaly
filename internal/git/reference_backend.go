package git

import (
	"bytes"
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

var (
	// ReferenceBackendReftables denotes the new binary format based
	// reftable backend.
	// See https://www.git-scm.com/docs/reftable for more details.
	ReferenceBackendReftables = ReferenceBackend{
		Name: "reftable",
	}

	// ReferenceBackendFiles denotes the traditional filesystem based
	// reference backend.
	ReferenceBackendFiles = ReferenceBackend{
		Name: "files",
	}
)

// ReferenceBackend is the specific backend implementation of references.
type ReferenceBackend struct {
	// Name is the name of the reference backend.
	Name string
}

// ReferenceBackendByName maps the output of `rev-parse --show-ref-format` to
// Gitaly's internal reference backend types.
func ReferenceBackendByName(name string) (ReferenceBackend, error) {
	switch name {
	case ReferenceBackendReftables.Name:
		return ReferenceBackendReftables, nil
	case ReferenceBackendFiles.Name:
		return ReferenceBackendFiles, nil
	default:
		return ReferenceBackend{}, fmt.Errorf("unknown reference backend: %q", name)
	}
}

// DetectReferenceBackend detects the reference backend used by the repository.
// If the git version doesn't support `--show-ref-format`, it'll simply echo
// '--show-ref-format'. We fallback to files backend in such situations.
func DetectReferenceBackend(ctx context.Context, gitCmdFactory CommandFactory, repository storage.Repository) (ReferenceBackend, error) {
	var stdout, stderr bytes.Buffer

	revParseCmd, err := gitCmdFactory.New(ctx, repository, Command{
		Name: "rev-parse",
		Flags: []Option{
			Flag{"--show-ref-format"},
		},
	}, WithStdout(&stdout), WithStderr(&stderr))
	if err != nil {
		return ReferenceBackend{}, fmt.Errorf("spawning rev-parse: %w", err)
	}

	if err := revParseCmd.Wait(); err != nil {
		return ReferenceBackend{}, structerr.New("reading reference backend: %w", err).WithMetadata("stderr", stderr.String())
	}

	backend, err := ReferenceBackendByName(text.ChompBytes(stdout.Bytes()))
	if err != nil {
		// If we don't know the backend type, let's just fallback to the files backend.
		return ReferenceBackendFiles, nil
	}
	return backend, nil
}
