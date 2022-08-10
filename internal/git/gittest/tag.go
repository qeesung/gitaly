package gittest

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

// WriteTagConfig holds extra options for WriteTag.
type WriteTagConfig struct {
	// Message is the message of an annotated tag. If left empty, then a lightweight tag will
	// be created.
	Message string
	// Force indicates whether existing tags with the same name shall be overwritten.
	Force bool
}

// WriteTag writes a new tag into the repository. This function either returns the tag ID in case
// an annotated tag was created, or otherwise the target object ID when a lightweight tag was
// created. Takes either no WriteTagConfig, in which case the default values will be used, or
// exactly one.
func WriteTag(
	tb testing.TB,
	cfg config.Cfg,
	repoPath string,
	tagName string,
	targetRevision git.Revision,
	optionalConfig ...WriteTagConfig,
) git.ObjectID {
	require.Less(tb, len(optionalConfig), 2, "only a single config may be passed")

	var config WriteTagConfig
	if len(optionalConfig) == 1 {
		config = optionalConfig[0]
	}

	args := []string{
		"-C", repoPath,
		"-c", fmt.Sprintf("user.name=%s", DefaultCommitterName),
		"-c", fmt.Sprintf("user.email=%s", DefaultCommitterMail),
		"tag",
	}

	if config.Force {
		args = append(args, "-f")
	}

	// The message can be very large, passing it directly in args would blow things up.
	stdin := bytes.NewBufferString(config.Message)
	if config.Message != "" {
		args = append(args, "-F", "-")
	}
	args = append(args, tagName, targetRevision.String())

	ExecOpts(tb, cfg, ExecConfig{Stdin: stdin}, args...)

	tagID := Exec(tb, cfg, "-C", repoPath, "show-ref", "-s", tagName)

	objectID, err := DefaultObjectHash.FromHex(text.ChompBytes(tagID))
	require.NoError(tb, err)

	return objectID
}
