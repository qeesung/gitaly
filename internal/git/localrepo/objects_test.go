package localrepo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

type ReaderFunc func([]byte) (int, error)

func (fn ReaderFunc) Read(b []byte) (int, error) { return fn(b) }

func TestRepo_WriteBlob(t *testing.T) {
	ctx := testhelper.Context(t)

	_, repo, repoPath := setupRepo(t)

	for _, tc := range []struct {
		desc       string
		attributes string
		input      io.Reader
		sha        string
		error      error
		content    string
	}{
		{
			desc:  "error reading",
			input: ReaderFunc(func([]byte) (int, error) { return 0, assert.AnError }),
			error: assert.AnError,
		},
		{
			desc:    "successful empty blob",
			input:   strings.NewReader(""),
			content: "",
		},
		{
			desc:    "successful blob",
			input:   strings.NewReader("some content"),
			content: "some content",
		},
		{
			desc:    "LF line endings left unmodified",
			input:   strings.NewReader("\n"),
			content: "\n",
		},
		{
			desc:    "CRLF converted to LF due to global git config",
			input:   strings.NewReader("\r\n"),
			content: "\n",
		},
		{
			desc:       "line endings preserved in binary files",
			input:      strings.NewReader("\r\n"),
			attributes: "file-path binary",
			content:    "\r\n",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			attributesPath := filepath.Join(repoPath, "info", "attributes")
			require.NoError(t, os.MkdirAll(filepath.Dir(attributesPath), 0o755))
			require.NoError(t, os.WriteFile(attributesPath, []byte(tc.attributes), os.ModePerm))

			sha, err := repo.WriteBlob(ctx, "file-path", tc.input)
			require.Equal(t, tc.error, err)
			if tc.error != nil {
				return
			}

			content, err := repo.ReadObject(ctx, sha)
			require.NoError(t, err)
			assert.Equal(t, tc.content, string(content))
		})
	}
}

func TestFormatTag(t *testing.T) {
	for _, tc := range []struct {
		desc       string
		objectID   git.ObjectID
		objectType string
		tagName    []byte
		tagBody    []byte
		author     *gitalypb.User
		authorDate time.Time
		err        error
	}{
		// Just trivial tests here, most of this is tested in
		// internal/gitaly/service/operations/tags_test.go
		{
			desc:       "basic signature",
			objectID:   gittest.DefaultObjectHash.ZeroOID,
			objectType: "commit",
			tagName:    []byte("my-tag"),
			author: &gitalypb.User{
				Name:  []byte("root"),
				Email: []byte("root@localhost"),
			},
			tagBody: []byte(""),
		},
		{
			desc:       "basic signature",
			objectID:   gittest.DefaultObjectHash.ZeroOID,
			objectType: "commit",
			tagName:    []byte("my-tag\ninjection"),
			tagBody:    []byte(""),
			author: &gitalypb.User{
				Name:  []byte("root"),
				Email: []byte("root@localhost"),
			},
			err: FormatTagError{expectedLines: 4, actualLines: 5},
		},
		{
			desc:       "signature with fixed time",
			objectID:   gittest.DefaultObjectHash.ZeroOID,
			objectType: "commit",
			tagName:    []byte("my-tag"),
			tagBody:    []byte(""),
			author: &gitalypb.User{
				Name:  []byte("root"),
				Email: []byte("root@localhost"),
			},
			authorDate: time.Unix(12345, 0),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			signature, err := FormatTag(tc.objectID, tc.objectType, tc.tagName, tc.tagBody, tc.author, tc.authorDate)
			if err != nil {
				require.Equal(t, tc.err, err)
				require.Equal(t, "", signature)
			} else {
				require.NoError(t, err)
				require.Contains(t, signature, "object ")
				require.Contains(t, signature, "tag ")
				require.Contains(t, signature, "tagger ")
			}
		})
	}
}

func TestRepo_WriteTag(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)

	commitID := gittest.WriteCommit(t, cfg, repoPath)

	for _, tc := range []struct {
		desc        string
		objectID    git.ObjectID
		objectType  string
		tagName     []byte
		tagBody     []byte
		author      *gitalypb.User
		authorDate  time.Time
		expectedTag string
	}{
		// Just trivial tests here, most of this is tested in
		// internal/gitaly/service/operations/tags_test.go
		{
			desc:       "basic signature",
			objectID:   commitID,
			objectType: "commit",
			tagName:    []byte("my-tag"),
			tagBody:    []byte(""),
			author: &gitalypb.User{
				Name:  []byte("root"),
				Email: []byte("root@localhost"),
			},
		},
		{
			desc:       "signature with time",
			objectID:   commitID,
			objectType: "commit",
			tagName:    []byte("tag-with-timestamp"),
			tagBody:    []byte(""),
			author: &gitalypb.User{
				Name:  []byte("root"),
				Email: []byte("root@localhost"),
			},
			authorDate: time.Unix(12345, 0).UTC(),
			expectedTag: fmt.Sprintf(`object %s
type commit
tag tag-with-timestamp
tagger root <root@localhost> 12345 +0000
`, commitID),
		},
		{
			desc:       "signature with time and timezone",
			objectID:   commitID,
			objectType: "commit",
			tagName:    []byte("tag-with-timezone"),
			tagBody:    []byte(""),
			author: &gitalypb.User{
				Name:  []byte("root"),
				Email: []byte("root@localhost"),
			},
			authorDate: time.Unix(12345, 0).In(time.FixedZone("myzone", -60*60)),
			expectedTag: fmt.Sprintf(`object %s
type commit
tag tag-with-timezone
tagger root <root@localhost> 12345 -0100
`, commitID),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tagObjID, err := repo.WriteTag(ctx, tc.objectID, tc.objectType, tc.tagName, tc.tagBody, tc.author, tc.authorDate)
			require.NoError(t, err)

			repoTagObjID := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", tagObjID.String())
			require.Equal(t, text.ChompBytes(repoTagObjID), tagObjID.String())

			if tc.expectedTag != "" {
				tag := gittest.Exec(t, cfg, "-C", repoPath, "cat-file", "-p", tagObjID.String())
				require.Equal(t, tc.expectedTag, text.ChompBytes(tag))
			}
		})
	}
}

func TestRepo_ReadObject(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)
	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("content"))

	for _, tc := range []struct {
		desc    string
		oid     git.ObjectID
		content string
		error   error
	}{
		{
			desc:  "invalid object",
			oid:   gittest.DefaultObjectHash.ZeroOID,
			error: InvalidObjectError(gittest.DefaultObjectHash.ZeroOID.String()),
		},
		{
			desc:    "valid object",
			oid:     blobID,
			content: "content",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			content, err := repo.ReadObject(ctx, tc.oid)
			require.Equal(t, tc.error, err)
			require.Equal(t, tc.content, string(content))
		})
	}
}

func TestRepo_ReadCommit(t *testing.T) {
	if gittest.ObjectHashIsSHA256() {
		t.Skip("this test is hash-agnostic, but depends on the `git/catfile` package that has not yet been converted")
	}

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)

	firstParentID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("first parent"))
	secondParentID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("second parent"))

	treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "file", Mode: "100644", Content: "content"},
	})
	commitWithoutTrailers := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(firstParentID, secondParentID),
		gittest.WithTree(treeID),
		gittest.WithMessage("subject\n\nbody\n"),
		gittest.WithBranch("main"),
	)
	commitWithTrailers := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(commitWithoutTrailers),
		gittest.WithTree(treeID),
		gittest.WithMessage("with trailers\n\ntrailers\n\nSigned-off-by: John Doe <john.doe@example.com>"),
	)

	// We can't use git-commit-tree(1) directly, but have to manually write signed commits.
	signedCommit := text.ChompBytes(gittest.ExecOpts(t, cfg, gittest.ExecConfig{
		Stdin: strings.NewReader(fmt.Sprintf(
			`tree %s
parent %s
author %[3]s
committer %[3]s
gpgsig -----BEGIN PGP SIGNATURE-----
some faked pgp-signature
 -----END PGP SIGNATURE-----

signed commit subject

signed commit body
`, treeID, firstParentID, gittest.DefaultCommitterSignature)),
	}, "-C", repoPath, "hash-object", "-t", "commit", "-w", "--stdin"))

	for _, tc := range []struct {
		desc           string
		revision       git.Revision
		opts           []ReadCommitOpt
		expectedCommit *gitalypb.GitCommit
		expectedErr    error
	}{
		{
			desc:        "invalid commit",
			revision:    gittest.DefaultObjectHash.ZeroOID.Revision(),
			expectedErr: ErrObjectNotFound,
		},
		{
			desc:        "invalid commit with trailers",
			revision:    gittest.DefaultObjectHash.ZeroOID.Revision(),
			expectedErr: ErrObjectNotFound,
			opts:        []ReadCommitOpt{WithTrailers()},
		},
		{
			desc:     "valid commit",
			revision: "refs/heads/main",
			expectedCommit: &gitalypb.GitCommit{
				Id:     commitWithoutTrailers.String(),
				TreeId: treeID.String(),
				ParentIds: []string{
					firstParentID.String(),
					secondParentID.String(),
				},
				Subject:   []byte("subject"),
				Body:      []byte("subject\n\nbody\n"),
				BodySize:  14,
				Author:    gittest.DefaultCommitAuthor,
				Committer: gittest.DefaultCommitAuthor,
			},
		},
		{
			desc:     "trailers do not get parsed without WithTrailers()",
			revision: commitWithTrailers.Revision(),
			expectedCommit: &gitalypb.GitCommit{
				Id:     commitWithTrailers.String(),
				TreeId: treeID.String(),
				ParentIds: []string{
					commitWithoutTrailers.String(),
				},
				Subject:   []byte("with trailers"),
				Body:      []byte("with trailers\n\ntrailers\n\nSigned-off-by: John Doe <john.doe@example.com>"),
				BodySize:  71,
				Author:    gittest.DefaultCommitAuthor,
				Committer: gittest.DefaultCommitAuthor,
			},
		},
		{
			desc:     "trailers get parsed with WithTrailers()",
			revision: commitWithTrailers.Revision(),
			opts:     []ReadCommitOpt{WithTrailers()},
			expectedCommit: &gitalypb.GitCommit{
				Id:     commitWithTrailers.String(),
				TreeId: treeID.String(),
				ParentIds: []string{
					commitWithoutTrailers.String(),
				},
				Subject:   []byte("with trailers"),
				Body:      []byte("with trailers\n\ntrailers\n\nSigned-off-by: John Doe <john.doe@example.com>"),
				BodySize:  71,
				Author:    gittest.DefaultCommitAuthor,
				Committer: gittest.DefaultCommitAuthor,
				Trailers: []*gitalypb.CommitTrailer{
					{
						Key:   []byte("Signed-off-by"),
						Value: []byte("John Doe <john.doe@example.com>"),
					},
				},
			},
		},
		{
			desc:     "with PGP signature",
			revision: git.Revision(signedCommit),
			opts:     []ReadCommitOpt{},
			expectedCommit: &gitalypb.GitCommit{
				Id:     signedCommit,
				TreeId: treeID.String(),
				ParentIds: []string{
					firstParentID.String(),
				},
				Subject:       []byte("signed commit subject"),
				Body:          []byte("signed commit subject\n\nsigned commit body\n"),
				BodySize:      42,
				Author:        gittest.DefaultCommitAuthor,
				Committer:     gittest.DefaultCommitAuthor,
				SignatureType: gitalypb.SignatureType_PGP,
			},
		},
		{
			desc:        "not a commit",
			revision:    "refs/heads/main^{tree}",
			expectedErr: ErrObjectNotFound,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			commit, err := repo.ReadCommit(ctx, tc.revision, tc.opts...)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedCommit, commit)
		})
	}
}

func TestRepo_IsAncestor(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)

	parentCommitID := gittest.WriteCommit(t, cfg, repoPath)
	childCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(parentCommitID))

	for _, tc := range []struct {
		desc         string
		parent       git.Revision
		child        git.Revision
		isAncestor   bool
		errorMatcher func(testing.TB, error)
	}{
		{
			desc:       "parent is ancestor",
			parent:     parentCommitID.Revision(),
			child:      childCommitID.Revision(),
			isAncestor: true,
		},
		{
			desc:       "parent is not ancestor",
			parent:     childCommitID.Revision(),
			child:      parentCommitID.Revision(),
			isAncestor: false,
		},
		{
			desc:   "parent is not valid commit",
			parent: gittest.DefaultObjectHash.ZeroOID.Revision(),
			child:  childCommitID.Revision(),
			errorMatcher: func(t testing.TB, err error) {
				require.Equal(t, InvalidCommitError(gittest.DefaultObjectHash.ZeroOID), err)
			},
		},
		{
			desc:   "child is not valid commit",
			parent: childCommitID.Revision(),
			child:  gittest.DefaultObjectHash.ZeroOID.Revision(),
			errorMatcher: func(t testing.TB, err error) {
				require.Equal(t, InvalidCommitError(gittest.DefaultObjectHash.ZeroOID), err)
			},
		},
		{
			desc:   "child points to a tree",
			parent: childCommitID.Revision(),
			child:  childCommitID.Revision() + "^{tree}",
			errorMatcher: func(t testing.TB, actualErr error) {
				treeOID, err := repo.ResolveRevision(ctx, childCommitID.Revision()+"^{tree}")
				require.NoError(t, err)
				require.EqualError(t, actualErr, fmt.Sprintf(
					`determine ancestry: exit status 128, stderr: "error: object %s is a tree, not a commit\nfatal: Not a valid commit name %s^{tree}\n"`,
					treeOID, childCommitID,
				))
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			isAncestor, err := repo.IsAncestor(ctx, tc.parent, tc.child)
			if tc.errorMatcher != nil {
				tc.errorMatcher(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.isAncestor, isAncestor)
		})
	}
}
