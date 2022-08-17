//go:build static && system_libgit2

package main

import (
	"context"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"
	"time"

	git "github.com/libgit2/git2go/v33"
	"gitlab.com/gitlab-org/gitaly/v15/cmd/gitaly-git2go/git2goutil"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
)

type mergeSubcommand struct{}

func (cmd *mergeSubcommand) Flags() *flag.FlagSet {
	return flag.NewFlagSet("merge", flag.ExitOnError)
}

func (cmd *mergeSubcommand) Run(_ context.Context, decoder *gob.Decoder, encoder *gob.Encoder) error {
	var request git2go.MergeCommand
	if err := decoder.Decode(&request); err != nil {
		return err
	}

	if request.AuthorDate.IsZero() {
		request.AuthorDate = time.Now()
	}

	commitID, err := cmd.merge(request)

	return encoder.Encode(git2go.Result{
		CommitID: commitID,
		Err:      git2go.SerializableError(err),
	})
}

func (cmd *mergeSubcommand) merge(request git2go.MergeCommand) (string, error) {
	repo, err := git2goutil.OpenRepository(request.Repository)
	if err != nil {
		return "", fmt.Errorf("could not open repository: %w", err)
	}
	defer repo.Free()

	ours, err := lookupCommit(repo, request.Ours)
	if err != nil {
		return "", fmt.Errorf("ours commit lookup: %w", err)
	}

	theirs, err := lookupCommit(repo, request.Theirs)
	if err != nil {
		return "", fmt.Errorf("theirs commit lookup: %w", err)
	}

	mergeOpts, err := git.DefaultMergeOptions()
	if err != nil {
		return "", fmt.Errorf("could not create merge options: %w", err)
	}
	mergeOpts.RecursionLimit = git2go.MergeRecursionLimit

	index, err := repo.MergeCommits(ours, theirs, &mergeOpts)
	if err != nil {
		return "", fmt.Errorf("could not merge commits: %w", err)
	}
	defer index.Free()

	if index.HasConflicts() {
		if !request.AllowConflicts {
			conflictingFiles, err := getConflictingFiles(index)
			if err != nil {
				return "", fmt.Errorf("getting conflicting files: %w", err)
			}

			return "", git2go.ConflictingFilesError{
				ConflictingFiles: conflictingFiles,
			}
		}

		if err := resolveConflicts(repo, index); err != nil {
			return "", fmt.Errorf("could not resolve conflicts: %w", err)
		}
	}

	treeOID, err := index.WriteTreeTo(repo)
	if err != nil {
		return "", fmt.Errorf("could not write tree: %w", err)
	}
	tree, err := repo.LookupTree(treeOID)
	if err != nil {
		return "", fmt.Errorf("lookup tree: %w", err)
	}

	author := git.Signature(git2go.NewSignature(request.AuthorName, request.AuthorMail, request.AuthorDate))
	committer := author
	if request.CommitterMail != "" {
		committer = git.Signature(git2go.NewSignature(request.CommitterName, request.CommitterMail, request.CommitterDate))
	}

	var parents []*git.Commit
	if request.Squash {
		parents = []*git.Commit{ours}
	} else {
		parents = []*git.Commit{ours, theirs}
	}

	commitID, err := git2goutil.NewCommitSubmitter(repo, request.SigningKey).
		Commit(&author, &committer, git.MessageEncodingUTF8, request.Message, tree, parents...)
	if err != nil {
		return "", fmt.Errorf("could not create merge commit: %w", err)
	}

	return commitID.String(), nil
}

func resolveConflicts(repo *git.Repository, index *git.Index) error {
	// We need to get all conflicts up front as resolving conflicts as we
	// iterate breaks the iterator.
	indexConflicts, err := getConflicts(index)
	if err != nil {
		return err
	}

	for _, conflict := range indexConflicts {
		if isConflictMergeable(conflict) {
			merge, err := Merge(repo, conflict)
			if err != nil {
				return err
			}

			mergedBlob, err := repo.CreateBlobFromBuffer(merge.Contents)
			if err != nil {
				return err
			}

			mergedIndexEntry := git.IndexEntry{
				Path: merge.Path,
				Mode: git.Filemode(merge.Mode),
				Id:   mergedBlob,
			}

			if err := index.Add(&mergedIndexEntry); err != nil {
				return err
			}

			if err := index.RemoveConflict(merge.Path); err != nil {
				return err
			}
		} else {
			if conflict.Their != nil {
				// If a conflict has `Their` present, we add it back to the index
				// as we want those changes to be part of the merge.
				if err := index.Add(conflict.Their); err != nil {
					return err
				}

				if err := index.RemoveConflict(conflict.Their.Path); err != nil {
					return err
				}
			} else if conflict.Our != nil {
				// If a conflict has `Our` present, remove its conflict as we
				// don't want to include those changes.
				if err := index.RemoveConflict(conflict.Our.Path); err != nil {
					return err
				}
			} else {
				// If conflict has no `Their` and `Our`, remove the conflict to
				// mark it as resolved.
				if err := index.RemoveConflict(conflict.Ancestor.Path); err != nil {
					return err
				}
			}
		}
	}

	if index.HasConflicts() {
		conflictingFiles, err := getConflictingFiles(index)
		if err != nil {
			return fmt.Errorf("getting conflicting files: %w", err)
		}

		return git2go.ConflictingFilesError{
			ConflictingFiles: conflictingFiles,
		}
	}

	return nil
}

func getConflictingFiles(index *git.Index) ([]string, error) {
	conflicts, err := getConflicts(index)
	if err != nil {
		return nil, fmt.Errorf("getting conflicts: %w", err)
	}

	conflictingFiles := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		switch {
		case conflict.Our != nil:
			conflictingFiles = append(conflictingFiles, conflict.Our.Path)
		case conflict.Ancestor != nil:
			conflictingFiles = append(conflictingFiles, conflict.Ancestor.Path)
		case conflict.Their != nil:
			conflictingFiles = append(conflictingFiles, conflict.Their.Path)
		default:
			return nil, errors.New("invalid conflict")
		}
	}

	return conflictingFiles, nil
}

func isConflictMergeable(conflict git.IndexConflict) bool {
	conflictIndexEntriesCount := 0

	if conflict.Their != nil {
		conflictIndexEntriesCount++
	}

	if conflict.Our != nil {
		conflictIndexEntriesCount++
	}

	if conflict.Ancestor != nil {
		conflictIndexEntriesCount++
	}

	return conflictIndexEntriesCount >= 2
}

func getConflicts(index *git.Index) ([]git.IndexConflict, error) {
	var conflicts []git.IndexConflict

	iterator, err := index.ConflictIterator()
	if err != nil {
		return nil, err
	}
	defer iterator.Free()

	for {
		conflict, err := iterator.Next()
		if err != nil {
			if git.IsErrorCode(err, git.ErrorCodeIterOver) {
				break
			}
			return nil, err
		}

		conflicts = append(conflicts, conflict)
	}

	return conflicts, nil
}
