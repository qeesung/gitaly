package localrepo

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/metadata"
)

type ReaderFunc func([]byte) (int, error)

func (fn ReaderFunc) Read(b []byte) (int, error) { return fn(b) }

func TestRepo_WriteBlob(t *testing.T) {
	t.Parallel()

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
			desc:    "CRLF not converted to LF due to global git config",
			input:   strings.NewReader("\r\n"),
			content: "\r\n",
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
			require.NoError(t, os.MkdirAll(filepath.Dir(attributesPath), perm.SharedDir))
			require.NoError(t, os.WriteFile(attributesPath, []byte(tc.attributes), perm.PublicFile))

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

func TestRepoReadObjectInfo(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath := setupRepo(t)
	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("content"))
	objectHash, err := repo.ObjectHash(ctx)
	require.NoError(t, err)

	for _, tc := range []struct {
		desc               string
		oid                git.ObjectID
		content            string
		expectedErr        error
		expectedObjectInfo catfile.ObjectInfo
	}{
		{
			desc:        "missing object",
			oid:         git.ObjectID("abcdefg"),
			expectedErr: InvalidObjectError("abcdefg"),
		},
		{
			desc:    "valid object",
			oid:     blobID,
			content: "content",
			expectedObjectInfo: catfile.ObjectInfo{
				Oid:    blobID,
				Type:   "blob",
				Size:   7,
				Format: objectHash.Format,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			info, err := repo.ReadObjectInfo(ctx, git.Revision(tc.oid))
			require.Equal(t, tc.expectedErr, err)
			if tc.expectedErr == nil {
				require.Equal(t, tc.expectedObjectInfo, *info)
			}
		})
	}
}

func TestRepo_ReadObject_catfileCount(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	gitCmdFactory := gittest.NewCountingCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	// Session needs to be set for the catfile cache to operate
	ctx = testhelper.MergeIncomingMetadata(ctx,
		metadata.Pairs(catfile.SessionIDField, "1"),
	)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := New(config.NewLocator(cfg), gitCmdFactory, catfileCache, repoProto)

	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("content"))

	for i := 0; i < 10; i++ {
		content, err := repo.ReadObject(ctx, blobID)
		require.NoError(t, err)
		require.Equal(t, "content", string(content))
	}

	gitCmdFactory.RequireCommandCount(t, "cat-file", 1)
}

func TestRepo_ReadCommit(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
			errorMatcher: func(tb testing.TB, err error) {
				require.Equal(tb, InvalidCommitError(gittest.DefaultObjectHash.ZeroOID), err)
			},
		},
		{
			desc:   "child is not valid commit",
			parent: childCommitID.Revision(),
			child:  gittest.DefaultObjectHash.ZeroOID.Revision(),
			errorMatcher: func(tb testing.TB, err error) {
				require.Equal(tb, InvalidCommitError(gittest.DefaultObjectHash.ZeroOID), err)
			},
		},
		{
			desc:   "child points to a tree",
			parent: childCommitID.Revision(),
			child:  childCommitID.Revision() + "^{tree}",
			errorMatcher: func(tb testing.TB, actualErr error) {
				treeOID, err := repo.ResolveRevision(ctx, childCommitID.Revision()+"^{tree}")
				require.NoError(tb, err)
				require.EqualError(tb, actualErr, fmt.Sprintf(
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

func TestWalkUnreachableObjects(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)

	commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("commit-1"))
	unreachableCommit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(commit1))
	unreachableCommit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(unreachableCommit1))
	prunedCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(unreachableCommit2))
	brokenParent1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(prunedCommit))

	// Pack brokenParent so we can unpack it into the repository as an object with broken links after
	// pruning.
	var packedBrokenParent bytes.Buffer
	require.NoError(t, repo.PackObjects(ctx, strings.NewReader(brokenParent1.String()), &packedBrokenParent))

	// Prune to remove the prunedCommit.
	gittest.Exec(t, cfg, "-C", repoPath, "prune", unreachableCommit1.String(), unreachableCommit2.String())

	// Unpack brokenParent now that the parent has been pruned.
	require.NoError(t, repo.UnpackObjects(ctx, &packedBrokenParent))

	require.ElementsMatch(t,
		[]git.ObjectID{
			gittest.DefaultObjectHash.EmptyTreeOID,
			commit1,
			unreachableCommit1,
			unreachableCommit2,
			brokenParent1,
		},
		gittest.ListObjects(t, cfg, repoPath),
	)

	for _, tc := range []struct {
		desc           string
		heads          []git.ObjectID
		expectedOutput []string
		expectedError  error
	}{
		{
			desc: "no heads",
		},
		{
			desc:  "reachable commit not reported",
			heads: []git.ObjectID{commit1},
		},
		{
			desc:  "unreachable commits reported",
			heads: []git.ObjectID{unreachableCommit2},
			expectedOutput: []string{
				unreachableCommit1.String(),
				unreachableCommit2.String(),
			},
		},
		{
			desc:          "non-existent head",
			heads:         []git.ObjectID{prunedCommit},
			expectedError: BadObjectError{ObjectID: prunedCommit},
		},
		{
			desc:          "traversal fails due to missing parent commit",
			heads:         []git.ObjectID{brokenParent1},
			expectedError: ObjectReadError{prunedCommit},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			var heads []string
			for _, head := range tc.heads {
				heads = append(heads, head.String())
			}

			var output bytes.Buffer
			require.Equal(t,
				tc.expectedError,
				repo.WalkUnreachableObjects(ctx, strings.NewReader(strings.Join(heads, "\n")), &output))

			var actualOutput []string
			if output.Len() > 0 {
				actualOutput = strings.Split(strings.TrimSpace(output.String()), "\n")
			}
			require.ElementsMatch(t, tc.expectedOutput, actualOutput)
		})
	}
}

func TestPackAndUnpackObjects(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)

	commit1 := gittest.WriteCommit(t, cfg, repoPath)
	commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(commit1))
	commit3 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(commit2))

	require.ElementsMatch(t,
		[]git.ObjectID{
			gittest.DefaultObjectHash.EmptyTreeOID,
			commit1,
			commit2,
			commit3,
		},
		gittest.ListObjects(t, cfg, repoPath),
	)

	var emptyPack bytes.Buffer
	require.NoError(t,
		repo.PackObjects(ctx, strings.NewReader(""),
			&emptyPack,
		),
	)

	var oneCommitPack bytes.Buffer
	require.NoError(t,
		repo.PackObjects(ctx, strings.NewReader(
			strings.Join([]string{commit1.String()}, "\n"),
		),
			&oneCommitPack,
		),
	)

	var twoCommitPack bytes.Buffer
	require.NoError(t,
		repo.PackObjects(ctx, strings.NewReader(
			strings.Join([]string{commit1.String(), commit2.String()}, "\n"),
		),
			&twoCommitPack,
		),
	)

	var incompletePack bytes.Buffer
	require.NoError(t,
		repo.PackObjects(ctx, strings.NewReader(
			strings.Join([]string{commit1.String(), commit3.String()}, "\n"),
		),
			&incompletePack,
		),
	)

	for _, tc := range []struct {
		desc                 string
		pack                 []byte
		expectedObjects      []git.ObjectID
		expectedErrorMessage string
	}{
		{
			desc: "empty pack",
			pack: emptyPack.Bytes(),
		},
		{
			desc: "one commit",
			pack: oneCommitPack.Bytes(),
			expectedObjects: []git.ObjectID{
				commit1,
			},
		},
		{
			desc: "two commits",
			pack: twoCommitPack.Bytes(),
			expectedObjects: []git.ObjectID{
				commit1, commit2,
			},
		},
		{
			desc: "incomplete pack",
			pack: incompletePack.Bytes(),
			expectedObjects: []git.ObjectID{
				commit1, commit3,
			},
		},
		{
			desc:                 "no pack",
			expectedErrorMessage: "unpack objects: exit status 128",
		},
		{
			desc:                 "broken pack",
			pack:                 []byte("invalid pack"),
			expectedErrorMessage: "unpack objects: exit status 128",
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg, repo, repoPath := setupRepo(t)
			require.Empty(t, gittest.ListObjects(t, cfg, repoPath))

			err := repo.UnpackObjects(ctx, bytes.NewReader(tc.pack))
			if tc.expectedErrorMessage != "" {
				require.EqualError(t, err, tc.expectedErrorMessage)
			} else {
				require.NoError(t, err)
			}
			require.ElementsMatch(t, tc.expectedObjects, gittest.ListObjects(t, cfg, repoPath))
		})
	}
}
