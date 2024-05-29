package conflict

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	type args struct {
		src           io.Reader
		ours          git.ObjectID
		theirs        git.ObjectID
		path          string
		resolution    Resolution
		appendNewLine bool
	}
	tests := []struct {
		name            string
		args            args
		resolvedContent []byte
		expectedErr     error
	}{
		{
			name: "select ours",
			args: args{
				src: strings.NewReader(fmt.Sprintf(`# this file is very conflicted
<<<<<<< %s
we want this line
=======
but they want this line
>>>>>>> %s
we can both agree on this line though
`, gittest.DefaultObjectHash.EmptyTreeOID, gittest.DefaultObjectHash.EmptyTreeOID)),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
				resolution: Resolution{
					NewPath: "conflict.txt",
					OldPath: "conflict.txt",
					Sections: map[string]string{
						"dc1c302824bab8da29f7c06fec1c77cf16b975e6_2_2": "head",
					},
				},
			},
			resolvedContent: []byte(`# this file is very conflicted
we want this line
we can both agree on this line though`),
		},
		{
			name: "select ours with newline",
			args: args{
				src: strings.NewReader(fmt.Sprintf(`# this file is very conflicted
<<<<<<< %s
we want this line
=======
but they want this line
>>>>>>> %s
we can both agree on this line though
`, gittest.DefaultObjectHash.EmptyTreeOID, gittest.DefaultObjectHash.EmptyTreeOID)),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
				resolution: Resolution{
					NewPath: "conflict.txt",
					OldPath: "conflict.txt",
					Sections: map[string]string{
						"dc1c302824bab8da29f7c06fec1c77cf16b975e6_2_2": "head",
					},
				},
				appendNewLine: true,
			},
			resolvedContent: []byte(`# this file is very conflicted
we want this line
we can both agree on this line though
`),
		},
		{
			name: "select theirs",
			args: args{
				src: strings.NewReader(fmt.Sprintf(`# this file is very conflicted
<<<<<<< %s
we want this line
=======
but they want this line
>>>>>>> %s
we can both agree on this line though
`, gittest.DefaultObjectHash.EmptyTreeOID, gittest.DefaultObjectHash.EmptyTreeOID)),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
				resolution: Resolution{
					NewPath: "conflict.txt",
					OldPath: "conflict.txt",
					Sections: map[string]string{
						"dc1c302824bab8da29f7c06fec1c77cf16b975e6_2_2": "origin",
					},
				},
			},
			resolvedContent: []byte(`# this file is very conflicted
but they want this line
we can both agree on this line though`),
		},
		{
			name: "CRLF conflicts with no newline",
			args: args{
				src: strings.NewReader(fmt.Sprintf(
					"# this file is very conflicted\r\n"+
						"<<<<<<< %s\r\n"+
						"we want this line\r\n"+
						"=======\r\n"+
						"but they want this line\r\n"+
						">>>>>>> %s\r\n"+
						"we can both agree on this line though\r\n",
					gittest.DefaultObjectHash.EmptyTreeOID, gittest.DefaultObjectHash.EmptyTreeOID,
				)),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
				resolution: Resolution{
					NewPath: "conflict.txt",
					OldPath: "conflict.txt",
					Sections: map[string]string{
						"dc1c302824bab8da29f7c06fec1c77cf16b975e6_2_2": "origin",
					},
				},
			},
			resolvedContent: []byte(
				"# this file is very conflicted\r\n" +
					"but they want this line\r\n" +
					"we can both agree on this line though",
			),
		},
		{
			name: "CRLF conflicts with newline",
			args: args{
				src: strings.NewReader(fmt.Sprintf(
					"# this file is very conflicted\r\n"+
						"<<<<<<< %s\r\n"+
						"we want this line\r\n"+
						"=======\r\n"+
						"but they want this line\r\n"+
						">>>>>>> %s\r\n"+
						"we can both agree on this line though\r\n",
					gittest.DefaultObjectHash.EmptyTreeOID, gittest.DefaultObjectHash.EmptyTreeOID,
				)),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
				resolution: Resolution{
					NewPath: "conflict.txt",
					OldPath: "conflict.txt",
					Sections: map[string]string{
						"dc1c302824bab8da29f7c06fec1c77cf16b975e6_2_2": "origin",
					},
				},
				appendNewLine: true,
			},
			resolvedContent: []byte(
				"# this file is very conflicted\r\n" +
					"but they want this line\r\n" +
					"we can both agree on this line though\r\n",
			),
		},
		{
			name: "UnexpectedDelimiter",
			args: args{
				src: strings.NewReader(fmt.Sprintf(`# this file is very conflicted
<<<<<<< %s
we want this line
<<<<<<< %s
=======
but they want this line
>>>>>>> %s
we can both agree on this line though
`, gittest.DefaultObjectHash.EmptyTreeOID, gittest.DefaultObjectHash.EmptyTreeOID, gittest.DefaultObjectHash.EmptyTreeOID)),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
			},

			expectedErr: fmt.Errorf("resolve: parse conflict for %q: %w", "conflict.txt", ErrUnexpectedDelimiter),
		},
		{
			name: "MissingEndDelimiter",
			args: args{
				src: strings.NewReader(fmt.Sprintf(`# this file is very conflicted
<<<<<<< %s
we want this line
=======
but they want this line
we can both agree on this line though
`, gittest.DefaultObjectHash.EmptyTreeOID)),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
			},

			expectedErr: fmt.Errorf("resolve: parse conflict for %q: %w", "conflict.txt", ErrMissingEndDelimiter),
		},
		{
			name: "Uses resolution.Content when there is no resolution sections",
			args: args{
				src:    strings.NewReader("foo\n"),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
				resolution: Resolution{
					OldPath: "conflict.txt",
					NewPath: "conflict.txt",
					Content: "bar\n",
				},
			},
			resolvedContent: []byte("bar\n"),
		},
		{
			name: "Conflict file under file limit",
			args: args{
				src:    strings.NewReader(strings.Repeat("x", fileLimit-2) + "\n"),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
			},
			resolvedContent: []byte(""),
		},
		{
			name: "Conflict file over file limit",
			args: args{
				src:    strings.NewReader(strings.Repeat("x", fileLimit+2) + "\n"),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
			},
			expectedErr: fmt.Errorf("resolve: parse conflict for %q: %w", "conflict.txt", ErrUnmergeableFile),
		},
		{
			name: "empty file",
			args: args{
				src:    strings.NewReader(""),
				ours:   gittest.DefaultObjectHash.EmptyTreeOID,
				theirs: gittest.DefaultObjectHash.EmptyTreeOID,
				path:   "conflict.txt",
			},
			expectedErr: fmt.Errorf("resolve: parse conflict for %q: %w", "conflict.txt", ErrUnmergeableFile),
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resolvedReader, err := Resolve(tt.args.src, tt.args.ours, tt.args.theirs, tt.args.path, tt.args.resolution, tt.args.appendNewLine)
			if err != nil || tt.expectedErr != nil {
				require.Equal(t, tt.expectedErr, err)
				return
			}

			resolvedContent, err := io.ReadAll(resolvedReader)
			require.NoError(t, err)
			require.Equal(t, tt.resolvedContent, resolvedContent)
		})
	}
}
