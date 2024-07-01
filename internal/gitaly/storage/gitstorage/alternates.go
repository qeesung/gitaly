package gitstorage

import (
	"errors"
	"fmt"
	"io/fs"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
)

var (
	// ErrNoAlternate is returned when a repository has no alternate.
	ErrNoAlternate = errors.New("repository has no alternate")
	// ErrMultipleAlternates is returned when a repository has multiple alternates
	// configured.
	ErrMultipleAlternates = errors.New("repository has multiple alternates")
)

// ReadAlternatesFile reads the alternates file from the given repository. ErrNoAlternate is returned if the
// file doesn't exist or didn't contain an alternate. ErrMultipleAlternates is returned if the
// repository had multiple alternates.
func ReadAlternatesFile(repositoryPath string) (string, error) {
	alternates, err := stats.ReadAlternatesFile(repositoryPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNoAlternate
		}

		return "", fmt.Errorf("read alternates file: %w", err)
	}

	if len(alternates) == 0 {
		return "", ErrNoAlternate
	} else if len(alternates) > 1 {
		// Repositories shouldn't have more than one alternate given they should only be
		// linked to a single pool at most.
		return "", ErrMultipleAlternates
	}

	return alternates[0], nil
}
