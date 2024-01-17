package localrepo

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// MergeStage denotes the stage indicated by git-merge-tree(1) in the conflicting
// files information section. The man page for git-merge(1) holds more information
// regarding the values of the stages and what they indicate.
type MergeStage uint

const (
	// MergeStageAncestor denotes a conflicting file version from the common ancestor.
	MergeStageAncestor = MergeStage(1)
	// MergeStageOurs denotes a conflicting file version from our commit.
	MergeStageOurs = MergeStage(2)
	// MergeStageTheirs denotes a conflicting file version from their commit.
	MergeStageTheirs = MergeStage(3)
)

// ErrMergeTreeUnrelatedHistory is used to denote the error when trying to merge two
// trees without unrelated history. This occurs when we don't use set the
// `allowUnrelatedHistories` option in the config.
var ErrMergeTreeUnrelatedHistory = errors.New("unrelated histories")

type mergeTreeConfig struct {
	allowUnrelatedHistories  bool
	conflictingFileNamesOnly bool
	mergeBase                git.Revision
}

// MergeTreeOption is a function that sets a config in mergeTreeConfig.
type MergeTreeOption func(*mergeTreeConfig)

// WithAllowUnrelatedHistories lets MergeTree accept two commits that do not
// share a common ancestor.
func WithAllowUnrelatedHistories() MergeTreeOption {
	return func(options *mergeTreeConfig) {
		options.allowUnrelatedHistories = true
	}
}

// WithConflictingFileNamesOnly lets MergeTree only parse the conflicting filenames and
// not the additional information.
func WithConflictingFileNamesOnly() MergeTreeOption {
	return func(options *mergeTreeConfig) {
		options.conflictingFileNamesOnly = true
	}
}

// WithMergeBase lets the caller pass a revision which will be passed with the
// --merge-base argument.
func WithMergeBase(base git.Revision) MergeTreeOption {
	return func(options *mergeTreeConfig) {
		options.mergeBase = base
	}
}

// MergeTree calls git-merge-tree(1) with arguments, and parses the results from
// stdout.
func (repo *Repo) MergeTree(
	ctx context.Context,
	ours, theirs string,
	mergeTreeOptions ...MergeTreeOption,
) (git.ObjectID, error) {
	var config mergeTreeConfig

	for _, option := range mergeTreeOptions {
		option(&config)
	}

	flags := []git.Option{
		git.Flag{Name: "-z"},
		git.Flag{Name: "--write-tree"},
	}

	if config.allowUnrelatedHistories {
		flags = append(flags, git.Flag{Name: "--allow-unrelated-histories"})
	}

	if config.conflictingFileNamesOnly {
		flags = append(flags, git.Flag{Name: "--name-only"})
	}

	if config.mergeBase != "" {
		flags = append(flags, git.ValueFlag{
			Name:  "--merge-base",
			Value: config.mergeBase.String(),
		})
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return "", structerr.NewInternal("getting object hash %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = repo.ExecAndWait(
		ctx,
		git.Command{
			Name:  "merge-tree",
			Flags: flags,
			Args:  []string{ours, theirs},
		},
		git.WithStderr(&stderr),
		git.WithStdout(&stdout),
	)
	if err != nil {
		exitCode, success := command.ExitStatus(err)
		if !success {
			return "", structerr.NewInternal("could not parse exit status of merge-tree(1)")
		}

		if exitCode == 1 {
			return parseMergeTreeError(objectHash, config, stdout.String())
		}

		if text.ChompBytes(stderr.Bytes()) == "fatal: refusing to merge unrelated histories" {
			return "", ErrMergeTreeUnrelatedHistory
		}

		return "", structerr.NewInternal("merge-tree: %w", err).WithMetadata("exit_status", exitCode)
	}

	oid, err := objectHash.FromHex(strings.Split(stdout.String(), "\x00")[0])
	if err != nil {
		return "", structerr.NewInternal("hex to oid: %w", err)
	}

	return oid, nil
}

// parseMergeTreeError parses the output from git-merge-tree(1)'s stdout into
// a MergeTreeResult struct. The format for the output can be found at
// https://git-scm.com/docs/git-merge-tree#OUTPUT.
func parseMergeTreeError(objectHash git.ObjectHash, cfg mergeTreeConfig, output string) (git.ObjectID, error) {
	var mergeTreeConflictError MergeTreeConflictError

	oidAndConflictsBuf, infoMsg, ok := strings.Cut(output, "\x00\x00")
	if !ok {
		return "", structerr.NewInternal("couldn't parse merge tree output: %s", output).WithMetadata("stderr", output)
	}

	oidAndConflicts := strings.Split(oidAndConflictsBuf, "\x00")

	oid, err := objectHash.FromHex(oidAndConflicts[0])
	if err != nil {
		return "", structerr.NewInternal("hex to oid: %w", err)
	}

	// If there are directory conflicts with unclear distinction, git-merge-tree(1)
	// doesn't output any filenames in the conflicted file info section
	if len(oidAndConflicts) > 1 {
		err := parseConflictingFileInfo(objectHash, cfg, &mergeTreeConflictError, oidAndConflicts[1:])
		if err != nil {
			return "", err
		}
	}

	fields := strings.Split(infoMsg, "\x00")
	// The git output contains a null character at the end, which creates a stray empty field.
	fields = fields[:len(fields)-1]

	for i := 0; i < len(fields); {
		c := ConflictInfoMessage{}

		numOfPaths, err := strconv.Atoi(fields[i])
		if err != nil {
			return "", structerr.NewInternal("converting stage to int: %w", err)
		}

		if i+numOfPaths+2 >= len(fields) {
			return "", structerr.NewInternal("incorrect number of fields: %s", infoMsg)
		}

		c.Paths = fields[i+1 : i+numOfPaths+1]
		c.Type = fields[i+numOfPaths+1]
		c.Message = fields[i+numOfPaths+2]

		mergeTreeConflictError.ConflictInfoMessage = append(mergeTreeConflictError.ConflictInfoMessage, c)

		i = i + numOfPaths + 3
	}

	return oid, &mergeTreeConflictError
}

func parseConflictingFileInfo(objectHash git.ObjectHash, cfg mergeTreeConfig, mergeTreeConflictError *MergeTreeConflictError, conflicts []string) error {
	mergeTreeConflictError.ConflictingFileInfo = make([]ConflictingFileInfo, len(conflicts))

	// From git-merge-tree(1), the information is of the format `<mode> <object> <stage> <filename>`
	// unless the `--name-only` option is used, in which case only the filename is output.
	// Note: that there is \t before the filename (https://gitlab.com/gitlab-org/git/blob/v2.40.0/builtin/merge-tree.c#L481)
	for i, infoLine := range conflicts {
		if cfg.conflictingFileNamesOnly {
			mergeTreeConflictError.ConflictingFileInfo[i].FileName = infoLine
		} else {
			infoAndFilename := strings.Split(infoLine, "\t")
			if len(infoAndFilename) != 2 {
				return structerr.NewInternal("parsing conflicting file info: %s", infoLine)
			}

			info := strings.Fields(infoAndFilename[0])
			if len(info) != 3 {
				return structerr.NewInternal("parsing conflicting file info: %s", infoLine)
			}

			mode, err := strconv.ParseInt(info[0], 8, 32)
			if err != nil {
				return structerr.NewInternal("parsing mode: %w", err)
			}

			mergeTreeConflictError.ConflictingFileInfo[i].OID, err = objectHash.FromHex(info[1])
			if err != nil {
				return structerr.NewInternal("hex to oid: %w", err)
			}

			stage, err := strconv.Atoi(info[2])
			if err != nil {
				return structerr.NewInternal("converting stage to int: %w", err)
			}

			if stage < 1 || stage > 3 {
				return structerr.NewInternal("invalid value for stage: %d", stage)
			}

			mergeTreeConflictError.ConflictingFileInfo[i].Mode = int32(mode)
			mergeTreeConflictError.ConflictingFileInfo[i].Stage = MergeStage(stage)
			mergeTreeConflictError.ConflictingFileInfo[i].FileName = infoAndFilename[1]
		}
	}

	return nil
}

// ConflictingFileInfo holds the conflicting file info output from git-merge-tree(1).
type ConflictingFileInfo struct {
	FileName string
	Mode     int32
	OID      git.ObjectID
	Stage    MergeStage
}

// ConflictInfoMessage holds the information message output from git-merge-tree(1).
type ConflictInfoMessage struct {
	Paths   []string
	Type    string
	Message string
}

// MergeTreeConflictError encapsulates any conflicting file info and messages that occur
// when a merge-tree(1) command fails.
type MergeTreeConflictError struct {
	ConflictingFileInfo []ConflictingFileInfo
	ConflictInfoMessage []ConflictInfoMessage
}

// Error returns the error string for a conflict error.
func (c *MergeTreeConflictError) Error() string {
	// TODO: for now, it's better that this error matches the git2go
	// error but once we deprecate the git2go code path in
	// merges, we can change this error to print out the conflicting files
	// and the InfoMessage.
	return "merge: there are conflicting files"
}

// ConflictedFiles is used to get the list of the names of the conflicted files from the
// MergeTreeConflictError.
func (c *MergeTreeConflictError) ConflictedFiles() []string {
	// We use a map for quick access to understand which files were already
	// accounted for.
	m := make(map[string]struct{})
	var files []string

	for _, fileInfo := range c.ConflictingFileInfo {
		if _, ok := m[fileInfo.FileName]; ok {
			continue
		}

		m[fileInfo.FileName] = struct{}{}
		files = append(files, fileInfo.FileName)
	}

	return files
}
