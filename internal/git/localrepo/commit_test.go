package localrepo

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/signature"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

//go:generate rm -rf testdata/gpg-keys testdata/signing_gpg_key testdata/signing_gpg_key.pub
//go:generate mkdir -p testdata/gpg-keys
//go:generate chmod 0700 testdata/gpg-keys
//go:generate gpg --homedir testdata/gpg-keys --generate-key --batch testdata/genkey.in
//go:generate gpg --homedir testdata/gpg-keys --export --output testdata/signing_gpg_key.pub
//go:generate gpg --homedir testdata/gpg-keys --export-secret-keys --output testdata/signing_gpg_key

func TestWriteCommit(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.GPGSigning).Run(t, testWriteCommit)
}

func testWriteCommit(t *testing.T, ctx context.Context) {
	t.Helper()
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	testcfg.BuildGitalyGPG(t, cfg)

	repo := NewTestRepo(t, cfg, repoProto)

	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("something"))
	changedBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("changed"))

	treeEntryA := TreeEntry{Path: "file", Mode: "100644", OID: blobID}
	treeA := &TreeEntry{
		Type:    Tree,
		Mode:    "040000",
		Entries: []*TreeEntry{&treeEntryA},
	}
	require.NoError(t, treeA.Write(
		ctx,
		repo))

	treeB := &TreeEntry{
		Type: Tree,
		Mode: "040000",
		Entries: []*TreeEntry{
			{Path: "file", Mode: "100644", OID: changedBlobID},
		},
	}
	require.NoError(t, treeB.Write(ctx, repo))
	commitA, err := repo.WriteCommit(
		ctx,
		WriteCommitConfig{
			AuthorName:     "Tazmanian Devil",
			AuthorEmail:    "taz@devils.org",
			CommitterName:  "Tazmanian Devil",
			CommitterEmail: "taz@devils.org",
			Message:        "I ❤️  Tazmania",
			TreeID:         treeA.OID,
		},
	)
	require.NoError(t, err)
	commitB, err := repo.WriteCommit(
		ctx,
		WriteCommitConfig{
			AuthorName:     "Daffy Duck",
			AuthorEmail:    "daffy@ducks.org",
			CommitterName:  "Daffy Duck",
			CommitterEmail: "daffy@ducks.org",
			Message:        "Big beak",
			TreeID:         treeB.OID,
		},
	)
	require.NoError(t, err)

	commitDate := time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		desc              string
		cfg               WriteCommitConfig
		expectedError     error
		expectedCommit    string
		expectedRevUpdate git.Revision
	}{
		{
			desc:          "missing tree",
			expectedError: ErrMissingTree,
		},
		{
			desc: "with commit message",
			cfg: WriteCommitConfig{
				TreeID:         treeA.OID,
				AuthorName:     "Scrooge Mcduck",
				AuthorEmail:    "chief@ducks.org",
				CommitterName:  "Mickey Mouse",
				CommitterEmail: "mickey@mouse.org",
				AuthorDate:     commitDate,
				CommitterDate:  commitDate,
				Message:        "my custom message\n\ntrailer\n",
			},
			expectedCommit: strings.Join([]string{
				"tree " + string(treeA.OID),
				"author Scrooge Mcduck <chief@ducks.org> " + git.FormatSignatureTime(commitDate),
				"committer Mickey Mouse <mickey@mouse.org> " + git.FormatSignatureTime(commitDate),
				"",
				"my custom message",
				"",
				"trailer",
			}, "\n"),
		},
		{
			desc: "with multiple parents",
			cfg: WriteCommitConfig{
				TreeID:         treeA.OID,
				Parents:        []git.ObjectID{commitA, commitB},
				AuthorName:     "Scrooge Mcduck",
				AuthorEmail:    "chief@ducks.org",
				CommitterName:  "Mickey Mouse",
				CommitterEmail: "mickey@mouse.org",
				AuthorDate:     commitDate,
				CommitterDate:  commitDate,
				Message:        "my custom message",
			},
			expectedCommit: strings.Join([]string{
				"tree " + treeA.OID.String(),
				"parent " + commitA.String(),
				"parent " + commitB.String(),
				"author Scrooge Mcduck <chief@ducks.org> " + git.FormatSignatureTime(commitDate),
				"committer Mickey Mouse <mickey@mouse.org> " + git.FormatSignatureTime(commitDate),
				"",
				"my custom message",
			}, "\n"),
		},
		{
			desc: "with reference",
			cfg: WriteCommitConfig{
				TreeID:         treeA.OID,
				Parents:        []git.ObjectID{commitA, commitB},
				AuthorName:     "Scrooge Mcduck",
				AuthorEmail:    "chief@ducks.org",
				CommitterName:  "Mickey Mouse",
				CommitterEmail: "mickey@mouse.org",
				AuthorDate:     commitDate,
				CommitterDate:  commitDate,
				Message:        "my custom message",
				Reference:      "refs/heads/foo",
			},
			expectedCommit: strings.Join([]string{
				"tree " + treeA.OID.String(),
				"parent " + commitA.String(),
				"parent " + commitB.String(),
				"author Scrooge Mcduck <chief@ducks.org> " + git.FormatSignatureTime(commitDate),
				"committer Mickey Mouse <mickey@mouse.org> " + git.FormatSignatureTime(commitDate),
				"",
				"my custom message",
			}, "\n"),
			expectedRevUpdate: "refs/heads/foo",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oid, err := repo.WriteCommit(ctx, tc.cfg)

			require.Equal(t, tc.expectedError, err)
			if err != nil {
				return
			}

			commit, err := repo.ReadObject(ctx, oid)
			require.NoError(t, err)

			require.Equal(t, tc.expectedCommit, text.ChompBytes(commit))

			if tc.expectedRevUpdate != "" {
				updatedOID := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", tc.expectedRevUpdate.String())
				require.Equal(t, oid, git.ObjectID(text.ChompBytes(updatedOID)))
			}
		})
	}

	t.Run("committer email and name are set", func(tb *testing.T) {
		testCases := []struct {
			desc                   string
			sign                   bool
			expectedCommitterName  string
			expectedCommitterEmail string
		}{
			{
				desc:                   "With commit signing",
				sign:                   true,
				expectedCommitterName:  "GitLab",
				expectedCommitterEmail: "noreply@gitlab.com",
			},
			{
				desc:                   "Without commit signing",
				sign:                   false,
				expectedCommitterName:  gittest.DefaultCommitterName,
				expectedCommitterEmail: gittest.DefaultCommitterMail,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.desc, func(t *testing.T) {
				cfg := WriteCommitConfig{
					TreeID:         treeA.OID,
					AuthorName:     gittest.DefaultCommitterName,
					AuthorEmail:    gittest.DefaultCommitterMail,
					CommitterName:  gittest.DefaultCommitterName,
					CommitterEmail: gittest.DefaultCommitterMail,
					AuthorDate:     gittest.DefaultCommitTime,
					CommitterDate:  gittest.DefaultCommitTime,
					Message:        "my custom message",
					GitConfig: config.Git{
						CommitterEmail: "noreply@gitlab.com",
						CommitterName:  "GitLab",
					},
					Sign: tc.sign,
				}

				oid, err := repo.WriteCommit(ctx, cfg)
				require.Nil(t, err)

				commit, err := repo.ReadCommit(ctx, git.Revision(oid))
				require.NoError(t, err)

				require.Equal(t, gittest.DefaultCommitAuthor, commit.Author)
				require.Equal(t, "my custom message", string(commit.Body))

				if featureflag.GPGSigning.IsEnabled(ctx) {
					require.Equal(t, tc.expectedCommitterEmail, string(commit.Committer.Email))
					require.Equal(t, tc.expectedCommitterName, string(commit.Committer.Name))
				} else {
					require.Equal(t, gittest.DefaultCommitterMail, string(commit.Committer.Email))
					require.Equal(t, gittest.DefaultCommitterName, string(commit.Committer.Name))
				}
			})
		}
	})

	t.Run("signed", func(tb *testing.T) {
		commitDate := time.Date(2023, time.November, 10, 23, 0, 0, 0, time.UTC)

		testCases := []struct {
			desc string
			sign bool
		}{
			{
				desc: "With commit signing",
				sign: true,
			},
			{
				desc: "Without commit signing",
				sign: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.desc, func(t *testing.T) {
				cfg := WriteCommitConfig{
					TreeID:         treeA.OID,
					AuthorName:     gittest.DefaultCommitterName,
					AuthorEmail:    gittest.DefaultCommitterMail,
					CommitterName:  gittest.DefaultCommitterName,
					CommitterEmail: gittest.DefaultCommitterMail,
					AuthorDate:     commitDate,
					CommitterDate:  commitDate,
					Message:        "my custom message",
					GitConfig: config.Git{
						SigningKey: "testdata/signing_gpg_key",
					},
					Sign: tc.sign,
				}

				oid, err := repo.WriteCommit(ctx, cfg)
				require.Nil(t, err)

				commit, err := repo.ReadCommit(ctx, git.Revision(oid))
				require.NoError(t, err)

				require.Equal(t, &gitalypb.CommitAuthor{
					Name:     []byte(gittest.DefaultCommitterName),
					Email:    []byte(gittest.DefaultCommitterMail),
					Date:     timestamppb.New(commitDate),
					Timezone: []byte("+0000"),
				}, commit.Author)
				require.Equal(t, "my custom message", string(commit.Body))

				data, err := repo.ReadObject(ctx, oid)
				require.NoError(t, err)

				gpgsig, dataWithoutGpgSig := signature.ExtractSignature(t, ctx, data)

				if featureflag.GPGSigning.IsEnabled(ctx) && tc.sign {
					pubKey := testhelper.MustReadFile(tb, "testdata/signing_gpg_key.pub")
					keyring, err := openpgp.ReadKeyRing(bytes.NewReader(pubKey))
					require.NoError(tb, err)

					_, err = openpgp.CheckArmoredDetachedSignature(
						keyring,
						strings.NewReader(dataWithoutGpgSig),
						strings.NewReader(gpgsig),
						&packet.Config{},
					)
					require.NoError(tb, err)
				} else {
					require.Empty(t, gpgsig)
				}
			})
		}
	})
}

func TestWriteCommit_validation(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.GPGSigning).Run(t, testWriteCommitValidation)
}

func testWriteCommitValidation(t *testing.T, ctx context.Context) {
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := NewTestRepo(t, cfg, repoProto)

	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("foo"))

	tree := &TreeEntry{
		Type: Tree,
		Mode: "040000",
		Entries: []*TreeEntry{
			{
				OID:  blobID,
				Mode: "100644",
				Path: "file1",
			},
		},
	}
	require.NoError(t, tree.Write(ctx, repo))
	treeID := tree.OID

	testCases := []struct {
		desc        string
		cfg         WriteCommitConfig
		expectedErr error
	}{
		{
			desc: "no author name",
			cfg: WriteCommitConfig{
				TreeID:         treeID,
				AuthorEmail:    "author@example.com",
				CommitterName:  "Coe Mitter",
				CommitterEmail: "coemitter@example.com",
			},
			expectedErr: ErrMissingAuthorName,
		},
		{
			desc: "no committer name",
			cfg: WriteCommitConfig{
				TreeID:         treeID,
				AuthorName:     "Awe Thor",
				AuthorEmail:    "author@example.com",
				CommitterEmail: "coemitter@example.com",
			},
			expectedErr: ErrMissingCommitterName,
		},
		{
			desc: "no tree",
			cfg: WriteCommitConfig{
				AuthorName:     "Awe Thor",
				AuthorEmail:    "author@example.com",
				CommitterName:  "Coe Mitter",
				CommitterEmail: "coemitter@example.com",
			},
			expectedErr: ErrMissingTree,
		},
		{
			desc: "author name consists of invalid characters",
			cfg: WriteCommitConfig{
				TreeID:         treeID,
				AuthorName:     ",",
				AuthorEmail:    "author@example.com",
				CommitterName:  "Coe Mitter",
				CommitterEmail: "coemitter@example.com",
			},
			expectedErr: ErrDisallowedCharacters,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := repo.WriteCommit(ctx, tc.cfg)
			require.Equal(t, tc.expectedErr, err)
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
