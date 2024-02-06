package testhelper

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
)

type tbRecorder struct {
	// Embed a nil TB as we'd rather panic if some calls that were
	// made were not captured by the recorder.
	testing.TB
	tb testing.TB

	errorMessage string
	helper       bool
	failNow      bool
}

func (r *tbRecorder) Name() string {
	return r.tb.Name()
}

func (r *tbRecorder) Errorf(format string, args ...any) {
	r.errorMessage = fmt.Sprintf(format, args...)
}

func (r *tbRecorder) Helper() {
	r.helper = true
}

func (r *tbRecorder) FailNow() {
	r.failNow = true
}

func (r *tbRecorder) Failed() bool {
	return r.errorMessage != ""
}

func TestRequireDirectoryState(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	relativePath := "assertion-root"
	umask := Umask()

	require.NoError(t,
		os.MkdirAll(
			filepath.Join(rootDir, relativePath, "dir-a"),
			fs.ModePerm,
		),
	)
	require.NoError(t,
		os.MkdirAll(
			filepath.Join(rootDir, relativePath, "dir-b"),
			perm.PrivateDir,
		),
	)
	require.NoError(t,
		os.WriteFile(
			filepath.Join(rootDir, relativePath, "dir-a", "unparsed-file"),
			[]byte("raw content"),
			fs.ModePerm,
		),
	)
	require.NoError(t,
		os.WriteFile(
			filepath.Join(rootDir, relativePath, "parsed-file"),
			[]byte("raw content"),
			perm.PrivateFile,
		),
	)

	for _, tc := range []struct {
		desc                 string
		modifyAssertion      func(DirectoryState)
		expectedErrorMessage string
	}{
		{
			desc:            "correct assertion",
			modifyAssertion: func(DirectoryState) {},
		},
		{
			desc: "unexpected directory",
			modifyAssertion: func(state DirectoryState) {
				delete(state, "/assertion-root")
			},
			expectedErrorMessage: `+ 	"/assertion-root":                     {Mode: s"drwxr-xr-x"}`,
		},
		{
			desc: "unexpected file",
			modifyAssertion: func(state DirectoryState) {
				delete(state, "/assertion-root/dir-a/unparsed-file")
			},
			expectedErrorMessage: `+ 	"/assertion-root/dir-a/unparsed-file": {Mode: s"-rwxr-xr-x", Content: []uint8("raw content")},`,
		},
		{
			desc: "wrong mode",
			modifyAssertion: func(state DirectoryState) {
				modified := state["/assertion-root/dir-b"]
				modified.Mode = fs.ModePerm
				state["/assertion-root/dir-b"] = modified
			},
			expectedErrorMessage: `- 		Mode:         s"-rwxrwxrwx",`,
		},
		{
			desc: "wrong unparsed content",
			modifyAssertion: func(state DirectoryState) {
				modified := state["/assertion-root/dir-a/unparsed-file"]
				modified.Content = "incorrect content"
				state["/assertion-root/dir-a/unparsed-file"] = modified
			},
			expectedErrorMessage: `- 		Content:      string("incorrect content"),
	            	+ 		Content:      []uint8("raw content"),`,
		},
		{
			desc: "wrong parsed content",
			modifyAssertion: func(state DirectoryState) {
				modified := state["/assertion-root/parsed-file"]
				modified.Content = "incorrect content"
				state["/assertion-root/parsed-file"] = modified
			},
			expectedErrorMessage: `- 		Content:      string("incorrect content"),
	            	+ 		Content:      string("parsed content"),`,
		},
		{
			desc: "missing entry",
			modifyAssertion: func(state DirectoryState) {
				state["/does/not/exist/on/disk"] = DirectoryEntry{}
			},
			expectedErrorMessage: `- 	"/does/not/exist/on/disk":     {}`,
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			expectedState := DirectoryState{
				"/assertion-root": {Mode: umask.Mask(fs.ModeDir | fs.ModePerm)},
				"/assertion-root/parsed-file": {
					Mode:    umask.Mask(perm.PrivateFile),
					Content: "parsed content",
					ParseContent: func(tb testing.TB, path string, content []byte) any {
						require.Equal(t, filepath.Join(rootDir, "/assertion-root/parsed-file"), path)
						return "parsed content"
					},
				},
				"/assertion-root/dir-a":               {Mode: umask.Mask(fs.ModeDir | fs.ModePerm)},
				"/assertion-root/dir-a/unparsed-file": {Mode: umask.Mask(fs.ModePerm), Content: []byte("raw content")},
				"/assertion-root/dir-b":               {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
			}

			tc.modifyAssertion(expectedState)

			recordedTB := &tbRecorder{tb: t}
			RequireDirectoryState(recordedTB, rootDir, relativePath, expectedState)

			if tc.expectedErrorMessage != "" {
				require.Contains(t,
					// The error message contains varying amounts of non-breaking space. Replace them with normal space
					// so they'll match our assertions.
					strings.Replace(recordedTB.errorMessage, "\u00a0", " ", -1),
					tc.expectedErrorMessage,
				)

				require.True(t, recordedTB.failNow)
			} else {
				require.Empty(t, recordedTB.errorMessage)
				require.False(t, recordedTB.failNow)
			}
			require.True(t, recordedTB.helper)
			require.NotNil(t,
				expectedState["/assertion-root/parsed-file"].ParseContent,
				"ParseContent should still be set on the original expected state",
			)
		})
	}
}
