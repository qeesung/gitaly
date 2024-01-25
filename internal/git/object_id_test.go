package git_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestObjectHashByFormat(t *testing.T) {
	for _, tc := range []struct {
		format             string
		expectedErr        error
		expectedObjectHash git.ObjectHash
	}{
		{
			format:             "sha1",
			expectedObjectHash: git.ObjectHashSHA1,
		},
		{
			format:             "sha256",
			expectedObjectHash: git.ObjectHashSHA256,
		},
		{
			format:      "invalid",
			expectedErr: fmt.Errorf("unknown object format: %q", "invalid"),
		},
	} {
		t.Run(tc.format, func(t *testing.T) {
			objectHash, err := git.ObjectHashByFormat(tc.format)
			require.Equal(t, tc.expectedErr, err)

			// Function pointers cannot be compared, so we need to unset them.
			objectHash.Hash = nil
			tc.expectedObjectHash.Hash = nil

			require.Equal(t, tc.expectedObjectHash, objectHash)
		})
	}
}

func TestObjectHashByProto(t *testing.T) {
	for _, tc := range []struct {
		desc               string
		objectFormat       gitalypb.ObjectFormat
		expectedErr        error
		expectedObjectHash git.ObjectHash
	}{
		{
			desc:               "unspecified object format",
			objectFormat:       gitalypb.ObjectFormat_OBJECT_FORMAT_UNSPECIFIED,
			expectedObjectHash: git.ObjectHashSHA1,
		},
		{
			desc:               "SHA1 object format",
			objectFormat:       gitalypb.ObjectFormat_OBJECT_FORMAT_SHA1,
			expectedObjectHash: git.ObjectHashSHA1,
		},
		{
			desc:               "SHA256 object format",
			objectFormat:       gitalypb.ObjectFormat_OBJECT_FORMAT_SHA256,
			expectedObjectHash: git.ObjectHashSHA256,
		},
		{
			desc:         "invalid object format",
			objectFormat: 3,
			expectedErr:  fmt.Errorf("unknown object format: \"3\""),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			objectHash, err := git.ObjectHashByProto(tc.objectFormat)
			require.Equal(t, tc.expectedErr, err)

			// Function pointers cannot be compared, so we need to unset them.
			objectHash.Hash = nil
			tc.expectedObjectHash.Hash = nil

			require.Equal(t, tc.expectedObjectHash, objectHash)
		})
	}
}

func TestDetectObjectHash(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	for _, tc := range []struct {
		desc         string
		setup        func(t *testing.T) *gitalypb.Repository
		expectedErr  error
		expectedHash git.ObjectHash
	}{
		{
			desc: "defaults to SHA1",
			setup: func(t *testing.T) *gitalypb.Repository {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					ObjectFormat:           "sha1",
				})

				// Verify that the repo doesn't explicitly mention it's using SHA1
				// as object hash.
				content := testhelper.MustReadFile(t, filepath.Join(repoPath, "config"))
				require.NotContains(t, text.ChompBytes(content), "sha1")

				return repo
			},
			expectedHash: git.ObjectHashSHA1,
		},
		{
			desc: "explicitly set to SHA1",
			setup: func(t *testing.T) *gitalypb.Repository {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					ObjectFormat:           "sha1",
				})

				// Explicitly set the object format to SHA1. Note that setting the
				// object format explicitly requires the repository format version
				// to be at least `1`.
				gittest.Exec(t, cfg, "-C", repoPath, "config", "core.repositoryFormatVersion", "1")
				gittest.Exec(t, cfg, "-C", repoPath, "config", "extensions.objectFormat", "sha1")

				return repo
			},
			expectedHash: git.ObjectHashSHA1,
		},
		{
			desc: "explicitly set to SHA256",
			setup: func(t *testing.T) *gitalypb.Repository {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					ObjectFormat:           "sha256",
				})

				require.Equal(t,
					"sha256",
					text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "config", "extensions.objectFormat")),
				)

				return repo
			},
			expectedHash: git.ObjectHashSHA256,
		},
		{
			desc: "invalid repository configuration",
			setup: func(t *testing.T) *gitalypb.Repository {
				testhelper.SkipWithReftable(t, "creating a repository with reftables sets the core.repositoryformatversion to 1")

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					ObjectFormat:           "sha1",
				})

				gittest.Exec(t, cfg, "-C", repoPath, "config", "extensions.objectFormat", "sha1")

				return repo
			},
			expectedErr: structerr.New("reading object format: exit status 128").WithMetadata(
				"stderr", "fatal: repo version is 0, but v1-only extension found:\n\tobjectformat\n",
			),
		},
		{
			desc: "unknown hash",
			setup: func(t *testing.T) *gitalypb.Repository {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				// Explicitly set the object format to something unknown.
				gittest.Exec(t, cfg, "-C", repoPath, "config", "extensions.objectFormat", "blake2")

				return repo
			},
			expectedErr: structerr.New("reading object format: exit status 128").WithMetadata(
				"stderr", "error: invalid value for 'extensions.objectformat'",
			),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto := tc.setup(t)

			hash, err := git.DetectObjectHash(ctx, gitCmdFactory, repoProto)
			if tc.expectedErr != nil {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr.Error())
			} else {
				require.NoError(t, err)
			}

			// Function pointers cannot be compared, so we need to unset them.
			hash.Hash = nil
			tc.expectedHash.Hash = nil

			require.Equal(t, tc.expectedHash, hash)
		})
	}
}

func TestObjectHash_ValidateHex(t *testing.T) {
	for _, hash := range []struct {
		desc     string
		hash     git.ObjectHash
		validHex string
	}{
		{
			desc:     "SHA1",
			hash:     git.ObjectHashSHA1,
			validHex: "a56e7793f9654d51dfb27312a1464062bceb9fa3",
		},
		{
			desc:     "SHA256",
			hash:     git.ObjectHashSHA256,
			validHex: "aec070645fe53ee3b3763059376134f058cc337247c978add178b6ccdfb0019f",
		},
	} {
		t.Run(hash.desc, func(t *testing.T) {
			for _, tc := range []struct {
				desc        string
				hex         string
				valid       bool
				expectedErr error
			}{
				{
					desc:  "valid object ID",
					hex:   hash.validHex,
					valid: true,
				},
				{
					desc:        "object ID with non-hex characters fails",
					hex:         "x" + hash.validHex[1:],
					valid:       false,
					expectedErr: git.InvalidObjectIDCharError{OID: "x" + hash.validHex[1:], BadChar: 'x'},
				},
				{
					desc:        "object ID with upper-case letters fails",
					hex:         strings.ToUpper(hash.validHex),
					valid:       false,
					expectedErr: git.InvalidObjectIDCharError{OID: strings.ToUpper(hash.validHex), BadChar: rune(strings.ToUpper(hash.validHex)[0])},
				},
				{
					desc:        "too short object ID fails",
					hex:         hash.validHex[:len(hash.validHex)-1],
					valid:       false,
					expectedErr: git.InvalidObjectIDLengthError{OID: hash.validHex[:len(hash.validHex)-1], CorrectLength: hash.hash.EncodedLen(), Length: len(hash.validHex) - 1},
				},
				{
					desc:        "too long object ID fails",
					hex:         hash.validHex + "3",
					valid:       false,
					expectedErr: git.InvalidObjectIDLengthError{OID: hash.validHex + "3", CorrectLength: hash.hash.EncodedLen(), Length: len(hash.validHex) + 1},
				},
				{
					desc:        "empty string fails",
					hex:         "",
					valid:       false,
					expectedErr: git.InvalidObjectIDLengthError{OID: "", CorrectLength: hash.hash.EncodedLen(), Length: 0},
				},
			} {
				t.Run(tc.desc, func(t *testing.T) {
					err := hash.hash.ValidateHex(tc.hex)
					require.Equal(t, err, tc.expectedErr)
				})
			}
		})
	}
}

func TestObjectHash_FromHex(t *testing.T) {
	for _, hash := range []struct {
		desc     string
		hash     git.ObjectHash
		validHex string
	}{
		{
			desc:     "SHA1",
			hash:     git.ObjectHashSHA1,
			validHex: "356e7793f9654d51dfb27312a1464062bceb9fa3",
		},
		{
			desc:     "SHA256",
			hash:     git.ObjectHashSHA256,
			validHex: "aec070645fe53ee3b3763059376134f058cc337247c978add178b6ccdfb0019f",
		},
	} {
		t.Run(hash.desc, func(t *testing.T) {
			for _, tc := range []struct {
				desc  string
				hex   string
				valid bool
			}{
				{
					desc:  "valid object ID",
					hex:   hash.validHex,
					valid: true,
				},
				{
					desc:  "object ID with non-hex characters fails",
					hex:   "x" + hash.validHex[1:],
					valid: false,
				},
				{
					desc:  "object ID with upper-case letters fails",
					hex:   strings.ToUpper(hash.validHex),
					valid: false,
				},
				{
					desc:  "too short object ID fails",
					hex:   hash.validHex[:len(hash.validHex)-1],
					valid: false,
				},
				{
					desc:  "too long object ID fails",
					hex:   hash.validHex + "3",
					valid: false,
				},
				{
					desc:  "empty string fails",
					hex:   "",
					valid: false,
				},
			} {
				t.Run(tc.desc, func(t *testing.T) {
					oid, err := hash.hash.FromHex(tc.hex)
					if tc.valid {
						require.NoError(t, err)
						require.Equal(t, tc.hex, oid.String())
					} else {
						require.Error(t, err)
					}
				})
			}
		})
	}
}

func TestObjectHash_EncodedLen(t *testing.T) {
	t.Parallel()
	require.Equal(t, 40, git.ObjectHashSHA1.EncodedLen())
	require.Equal(t, 64, git.ObjectHashSHA256.EncodedLen())
}

func TestObjectHash_HashData(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc              string
		data              []byte
		expectedSHA1OID   git.ObjectID
		expectedSHA256OID git.ObjectID
	}{
		{
			desc:              "nil slice",
			data:              nil,
			expectedSHA1OID:   "da39a3ee5e6b4b0d3255bfef95601890afd80709",
			expectedSHA256OID: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			desc:              "empty slice",
			data:              []byte{},
			expectedSHA1OID:   "da39a3ee5e6b4b0d3255bfef95601890afd80709",
			expectedSHA256OID: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			desc:              "some data",
			data:              []byte("some data"),
			expectedSHA1OID:   "baf34551fecb48acc3da868eb85e1b6dac9de356",
			expectedSHA256OID: "1307990e6ba5ca145eb35e99182a9bec46531bc54ddf656a602c780fa0240dee",
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Run("sha1", func(t *testing.T) {
				require.Equal(t, tc.expectedSHA1OID, git.ObjectHashSHA1.HashData(tc.data))
			})

			t.Run("sha256", func(t *testing.T) {
				require.Equal(t, tc.expectedSHA256OID, git.ObjectHashSHA256.HashData(tc.data))
			})
		})
	}
}

func TestObjectID_Bytes(t *testing.T) {
	for _, tc := range []struct {
		desc          string
		oid           git.ObjectID
		expectedBytes []byte
		expectedErr   error
	}{
		{
			desc:          "zero OID",
			oid:           git.ObjectHashSHA1.ZeroOID,
			expectedBytes: bytes.Repeat([]byte{0}, 20),
		},
		{
			desc:          "valid object ID",
			oid:           git.ObjectID(strings.Repeat("8", 40)),
			expectedBytes: bytes.Repeat([]byte{0x88}, 20),
		},
		{
			desc:        "invalid object ID",
			oid:         git.ObjectID(strings.Repeat("8", 39) + "x"),
			expectedErr: hex.InvalidByteError('x'),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			actualBytes, err := tc.oid.Bytes()
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedBytes, actualBytes)
		})
	}
}

func TestObjectHash_IsZeroOID(t *testing.T) {
	for _, hash := range []struct {
		desc     string
		hash     git.ObjectHash
		validHex string
	}{
		{
			desc: "SHA1",
			hash: git.ObjectHashSHA1,
		},
		{
			desc: "SHA256",
			hash: git.ObjectHashSHA256,
		},
	} {
		t.Run(hash.desc, func(t *testing.T) {
			for _, tc := range []struct {
				desc   string
				oid    git.ObjectID
				isZero bool
			}{
				{
					desc:   "zero object ID",
					oid:    hash.hash.ZeroOID,
					isZero: true,
				},
				{
					desc:   "zero object ID",
					oid:    hash.hash.EmptyTreeOID,
					isZero: false,
				},
			} {
				t.Run(tc.desc, func(t *testing.T) {
					require.Equal(t, tc.isZero, hash.hash.IsZeroOID(tc.oid))
				})
			}
		})
	}
}
