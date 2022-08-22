package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"
)

// PruneOldGitalyProcessDirectories removes leftover temporary directories that belonged to processes that
// no longer exist. Directories are expected to be in the form gitaly-<pid>.
// The removals are logged prior to being executed. Unexpected directory entries are logged
// but not removed.
func PruneOldGitalyProcessDirectories(log log.FieldLogger, directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("list gitaly process directory: %w", err)
	}

	for _, entry := range entries {
		log := log.WithField("path", filepath.Join(directory, entry.Name()))
		if err := func() error {
			if !entry.IsDir() {
				// There should be no files, only the gitaly process directories.
				return errors.New("gitaly process directory contains an unexpected file")
			}

			components := strings.Split(entry.Name(), "-")
			if len(components) != 2 || components[0] != "gitaly" {
				// This directory does not match the gitaly process directory naming format
				// of `gitaly-<process id>.
				return errors.New("gitaly process directory contains an unexpected directory")
			}

			processID, err := strconv.ParseInt(components[1], 10, 64)
			if err != nil {
				// This is not a temporary gitaly process directory as the section
				// after the hyphen is not a process id.
				return errors.New("gitaly process directory contains an unexpected directory")
			}

			process, err := os.FindProcess(int(processID))
			if err != nil {
				return fmt.Errorf("could not find process: %w", err)
			}

			defer func() {
				if err := process.Release(); err != nil {
					log.WithError(err).Error("failed releasing process")
				}
			}()

			if err := process.Signal(syscall.Signal(0)); err != nil {
				// Either the process does not exist, or the pid has been re-used by for a
				// process owned by another user and is not a Gitaly process.
				if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.EPERM) {
					return fmt.Errorf("sending signal 0 to process: %w", err)
				}

				if err := os.RemoveAll(filepath.Join(directory, entry.Name())); err != nil {
					return fmt.Errorf("removing leftover gitaly process directory: %w", err)
				}

				log.Info("removed leftover gitaly process directory")
			}

			return nil
		}(); err != nil {
			log.WithError(err).Error("could not prune entry")
			continue
		}
	}

	return nil
}

// GetGitalyProcessTempDir constructs a temporary directory name for the current gitaly
// process. This way, we can clean up old temporary directories by inspecting the pid attached
// to the folder.
func GetGitalyProcessTempDir(parentDir string, processID int) string {
	return filepath.Join(parentDir, fmt.Sprintf("gitaly-%d", processID))
}
