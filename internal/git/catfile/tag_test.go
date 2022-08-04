package catfile

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestGetTag(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, objectReader, _, repoPath := setupObjectReader(t, ctx)
	commitID := gittest.WriteCommit(t, cfg, repoPath)

	for _, tc := range []struct {
		tagName string
		message string
	}{
		{
			tagName: fmt.Sprintf("%s-v1.0.2", t.Name()),
			message: strings.Repeat("a", helper.MaxCommitOrTagMessageSize+1) + "\n",
		},
		{
			tagName: fmt.Sprintf("%s-v1.0.0", t.Name()),
			message: "Prod Release v1.0.0\n",
		},
		{
			tagName: fmt.Sprintf("%s-v1.0.1", t.Name()),
			message: strings.Repeat("a", helper.MaxCommitOrTagMessageSize+1) + "\n",
		},
	} {
		t.Run(tc.tagName, func(t *testing.T) {
			tagID := gittest.WriteTag(t, cfg, repoPath, tc.tagName, commitID.Revision(), gittest.WriteTagConfig{Message: tc.message})

			tag, err := GetTag(ctx, objectReader, git.Revision(tagID), tc.tagName)
			require.NoError(t, err)
			require.Equal(t, tc.message, string(tag.Message))
			require.Equal(t, tc.tagName, string(tag.GetName()))
		})
	}
}

func TestTrimTag(t *testing.T) {
	for _, tc := range []struct {
		desc                string
		message             string
		expectedMessage     string
		expectedMessageSize int
	}{
		{
			desc:                "simple short message",
			message:             "foo",
			expectedMessage:     "foo",
			expectedMessageSize: 3,
		},
		{
			desc:                "trailing newlines",
			message:             "foo\n\n",
			expectedMessage:     "foo",
			expectedMessageSize: 3,
		},
		{
			desc:                "too long",
			message:             strings.Repeat("a", helper.MaxCommitOrTagMessageSize+1),
			expectedMessage:     strings.Repeat("a", helper.MaxCommitOrTagMessageSize),
			expectedMessageSize: helper.MaxCommitOrTagMessageSize + 1,
		},
		{
			desc:                "too long with newlines at cutoff",
			message:             strings.Repeat("a", helper.MaxCommitOrTagMessageSize-1) + "\nsomething",
			expectedMessage:     strings.Repeat("a", helper.MaxCommitOrTagMessageSize-1) + "\n",
			expectedMessageSize: helper.MaxCommitOrTagMessageSize - 1 + len("\nsomething"),
		},
		{
			desc:                "too long with trailing newline",
			message:             strings.Repeat("a", helper.MaxCommitOrTagMessageSize) + "foo\n",
			expectedMessage:     strings.Repeat("a", helper.MaxCommitOrTagMessageSize),
			expectedMessageSize: helper.MaxCommitOrTagMessageSize + len("foo"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tag := &gitalypb.Tag{
				Message: []byte(tc.message),
			}
			TrimTagMessage(tag)
			require.Equal(t, tc.expectedMessage, string(tag.Message))
			require.Equal(t, int64(tc.expectedMessageSize), tag.MessageSize)
		})
	}
}
