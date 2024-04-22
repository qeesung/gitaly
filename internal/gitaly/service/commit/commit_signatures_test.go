package commit

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	pgpSignature = `-----BEGIN PGP SIGNATURE-----
Version: ObjectivePGP
Comment: https://www.objectivepgp.com
Charset: UTF-8

wsFcBAABCgAGBQJecon1AAoJEDYMjTn1G2THmSsP/At/jskLdF0i7p0nKf4JLjeeqRJ4k2IUg87U
ZwV6mbLo5XFm8Sq7CJBAGAhlOZE4BAwKALuawmgs5XMEZwK2z6AIgosGTVpmxDTTI11bXt4XIOdz
qF7c/gUrJOZzjFXOqDsd5UuPRupwznC5eKlLbfImR+NYxKryo8JGdF5t52ph4kChcQsKlSkXuYNI
+9UgbaMclEjb0OLm+mcP9QxW+Cs9JS2Jb4Jh6XONWW1nDN3ZTDDskguIqqF47UxIgSImrmpMcEj9
YSNU0oMoHM4+1DoXp1t99EGPoAMvO+a5g8gd1jouCIrI6KOX+GeG/TFFM0mQwg/d/N9LR049m8ed
vgqg/lMiWUxQGL2IPpYPcgiUEqfn7ete+NMzQV5zstxF/q7Yj2BhM2L7FPHxKaoy/w5Q/DcAO4wN
5gxVmIvbCDk5JOx8I+boIS8ZxSvIlJ5IWaPrcjg5Mc40it+WHvMqxVnCzH0c6KcXaJ2SibVb59HR
pdRhEXXw/hRN65l/xwyM8sklQalAGu755gNJZ4k9ApBVUssZyiu+te2+bDirAcmK8/x1jvMQY6bn
DFxBE7bMHDp24IFPaVID84Ryt3vSSBEkrUGm7OkyDESTpHCr4sfD5o3LCUCIibTqv/CAhe59mhbB
2AXL7X+EzylKy6C1N5KUUiMTW94AuF6f8FqBoxnf
=U6zM
-----END PGP SIGNATURE-----`

	sshSignature = `-----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgtc+Qk8jhMwVZk/jFEFCM16LNQb
30q5kK30bbetfjyTMAAAADZ2l0AAAAAAAAAAZzaGE1MTIAAABTAAAAC3NzaC1lZDI1NTE5
AAAAQADE1oOMKxqQu86XUQbhCoWx8GnnYHQ/i3mHdA0zPycIlDv8N6BRVDS6b0ja2Avj+s
uNvjRqSEGQJ4q6vhKOnQw=
-----END SSH SIGNATURE-----`
)

func TestGetCommitSignatures(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.GPGSigning).Run(t, testGetCommitSignatures)
}

func testGetCommitSignatures(t *testing.T, ctx context.Context) {
	cfg := testcfg.Build(t)
	testcfg.BuildGitalyGPG(t, cfg)

	cfg.Git.SigningKey = "testdata/signing_ssh_key_ed25519"
	cfg.Git.RotatedSigningKeys = []string{"testdata/signing_ssh_key_rsa"}
	cfg.SocketPath = startTestServices(t, cfg)
	client := newCommitServiceClient(t, cfg.SocketPath)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

	type setupData struct {
		request           *gitalypb.GetCommitSignaturesRequest
		expectedErr       error
		expectedResponses []*gitalypb.GetCommitSignaturesResponse
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: nil,
						CommitIds:  []string{"5937ac0a7beb003549fc5fd26fc247adbce4a52e"},
					},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "unset commit IDs",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds:  []string{},
					},
					expectedErr: status.Error(codes.InvalidArgument, "empty CommitIds"),
				}
			},
		},
		{
			desc: "abbreviated commit ID",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							gittest.DefaultObjectHash.HashData([]byte("pseudo commit")).String(),
							"a17a9f6",
						},
					},
					expectedErr: structerr.NewInvalidArgument(`invalid object ID: "a17a9f6", expected length %v, got 7`, gittest.DefaultObjectHash.EncodedLen()),
				}
			},
		},
		{
			desc: "nonexistent commit",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							gittest.DefaultObjectHash.HashData([]byte("nonexistent commit")).String(),
						},
					},
				}
			},
		},
		{
			desc: "unsigned commit",
			setup: func(t *testing.T) setupData {
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "PGP-signed commit",
			setup: func(t *testing.T) setupData {
				commitID, commitData := createCommitWithSignature(t, cfg, repoPath, "gpgsig", pgpSignature, "Signed commit message\n")

				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							commitID.String(),
						},
					},
					expectedResponses: []*gitalypb.GetCommitSignaturesResponse{
						{
							CommitId:   commitID.String(),
							Signature:  []byte(pgpSignature),
							SignedText: []byte(commitData),
							Signer:     gitalypb.GetCommitSignaturesResponse_SIGNER_USER,
						},
					},
				}
			},
		},
		{
			desc: "PGP-signed commit with message lacking newline",
			setup: func(t *testing.T) setupData {
				commitID, commitData := createCommitWithSignature(t, cfg, repoPath, "gpgsig", pgpSignature, "Signed commit message")

				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							commitID.String(),
						},
					},
					expectedResponses: []*gitalypb.GetCommitSignaturesResponse{
						{
							CommitId:   commitID.String(),
							Signature:  []byte(pgpSignature),
							SignedText: []byte(commitData),
							Signer:     gitalypb.GetCommitSignaturesResponse_SIGNER_USER,
						},
					},
				}
			},
		},
		{
			desc: "PGP-signed commit with huge commit message",
			setup: func(t *testing.T) setupData {
				commitID, commitData := createCommitWithSignature(t, cfg, repoPath, "gpgsig", pgpSignature, strings.Repeat("a", 5*1024*1024))

				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							commitID.String(),
						},
					},
					expectedResponses: []*gitalypb.GetCommitSignaturesResponse{
						{
							CommitId:   commitID.String(),
							Signature:  []byte(pgpSignature),
							SignedText: []byte(commitData),
							Signer:     gitalypb.GetCommitSignaturesResponse_SIGNER_USER,
						},
					},
				}
			},
		},
		{
			desc: "SSH-signed commit",
			setup: func(t *testing.T) setupData {
				commitID, commitData := createCommitWithSignature(t, cfg, repoPath, "gpgsig", sshSignature, "SSH-signed commit message\n")

				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							commitID.String(),
						},
					},
					expectedResponses: []*gitalypb.GetCommitSignaturesResponse{
						{
							CommitId:   commitID.String(),
							Signature:  []byte(sshSignature),
							SignedText: []byte(commitData),
							Signer:     gitalypb.GetCommitSignaturesResponse_SIGNER_USER,
						},
					},
				}
			},
		},
		{
			desc: "garbage-signed commit",
			setup: func(t *testing.T) setupData {
				commitID, _ := createCommitWithSignature(t, cfg, repoPath, "gpgsig-garbage", sshSignature, "garbage-signed commit message")

				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							commitID.String(),
						},
					},
				}
			},
		},
		{
			desc: "SHA256-signed commit",
			setup: func(t *testing.T) setupData {
				commitID, commitData := createCommitWithSignature(t, cfg, repoPath, "gpgsig-sha256", sshSignature, "sha256-signed commit message")

				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							commitID.String(),
						},
					},
					expectedResponses: []*gitalypb.GetCommitSignaturesResponse{
						{
							CommitId:   commitID.String(),
							Signature:  []byte(sshSignature),
							SignedText: []byte(commitData),
							Signer:     gitalypb.GetCommitSignaturesResponse_SIGNER_USER,
						},
					},
				}
			},
		},
		{
			desc: "signed by Gitaly",
			setup: func(t *testing.T) setupData {
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("updated"))

				tree := &localrepo.TreeEntry{
					Type: localrepo.Tree,
					Mode: "040000",
					Entries: []*localrepo.TreeEntry{
						{Path: "file", Mode: "100644", OID: blobID},
					},
				}
				require.NoError(t, tree.Write(ctx, repo))

				commitID, err := repo.WriteCommit(ctx, localrepo.WriteCommitConfig{
					TreeID:         tree.OID,
					AuthorName:     gittest.DefaultCommitterName,
					AuthorEmail:    gittest.DefaultCommitterMail,
					CommitterName:  gittest.DefaultCommitterName,
					CommitterEmail: gittest.DefaultCommitterMail,
					AuthorDate:     gittest.DefaultCommitTime,
					CommitterDate:  gittest.DefaultCommitTime,
					Message:        "message",
					GitConfig:      cfg.Git,
					Sign:           featureflag.GPGSigning.IsEnabled(ctx),
				})
				require.NoError(t, err)

				rotatedKeyCommitID, err := repo.WriteCommit(ctx, localrepo.WriteCommitConfig{
					TreeID:         tree.OID,
					AuthorName:     gittest.DefaultCommitterName,
					AuthorEmail:    gittest.DefaultCommitterMail,
					CommitterName:  gittest.DefaultCommitterName,
					CommitterEmail: gittest.DefaultCommitterMail,
					AuthorDate:     gittest.DefaultCommitTime,
					CommitterDate:  gittest.DefaultCommitTime,
					Message:        "rotated key commit message",
					GitConfig: config.Git{
						SigningKey: cfg.Git.RotatedSigningKeys[0],
					},
					Sign: featureflag.GPGSigning.IsEnabled(ctx),
				})
				require.NoError(t, err)

				rsaSHA1Signature := string(testhelper.MustReadFile(t, "testdata/signing_ssh_key_rsa.sig"))
				rsaSHA256Signature := string(testhelper.MustReadFile(t, "testdata/signing_ssh_key_rsa_sha256.sig"))
				ed25519SHA1Signature := string(testhelper.MustReadFile(t, "testdata/signing_ssh_key_ed25519.sig"))
				ed25519SHA256Signature := string(testhelper.MustReadFile(t, "testdata/signing_ssh_key_ed25519_sha256.sig"))

				return setupData{
					request: &gitalypb.GetCommitSignaturesRequest{
						Repository: repoProto,
						CommitIds: []string{
							commitID.String(),
							rotatedKeyCommitID.String(),
						},
					},
					expectedResponses: testhelper.EnabledOrDisabledFlag(ctx, featureflag.GPGSigning,
						[]*gitalypb.GetCommitSignaturesResponse{
							{
								CommitId: commitID.String(),
								Signature: []byte(gittest.ObjectHashDependent(t, map[string]string{
									"sha1":   ed25519SHA1Signature,
									"sha256": ed25519SHA256Signature,
								})),
								SignedText: []byte(fmt.Sprintf(
									"tree %s\nauthor %s\ncommitter %s\n\nmessage",
									tree.OID,
									gittest.DefaultCommitterSignature,
									gittest.DefaultCommitterSignature,
								)),
								Signer: gitalypb.GetCommitSignaturesResponse_SIGNER_SYSTEM,
							},
							{
								CommitId: rotatedKeyCommitID.String(),
								Signature: []byte(gittest.ObjectHashDependent(t, map[string]string{
									"sha1":   rsaSHA1Signature,
									"sha256": rsaSHA256Signature,
								})),
								SignedText: []byte(fmt.Sprintf(
									"tree %s\nauthor %s\ncommitter %s\n\nrotated key commit message",
									tree.OID,
									gittest.DefaultCommitterSignature,
									gittest.DefaultCommitterSignature,
								)),
								Signer: gitalypb.GetCommitSignaturesResponse_SIGNER_SYSTEM,
							},
						},
						nil,
					),
				}
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			setup := tc.setup(t)

			stream, err := client.GetCommitSignatures(ctx, setup.request)
			require.NoError(t, err)

			actualResponses, err := testhelper.ReceiveAndFold(stream.Recv, func(
				result []*gitalypb.GetCommitSignaturesResponse,
				response *gitalypb.GetCommitSignaturesResponse,
			) []*gitalypb.GetCommitSignaturesResponse {
				// We don't need to do any fiddling when we have a commit ID, which would signify
				// another returned commit.
				if response.CommitId != "" {
					return append(result, response)
				}

				// But when we don't have a commit ID we append both the signature and signed text so
				// that it becomes easier to test for these values, as they might otherwise be split.
				currentResponse := result[len(result)-1]
				currentResponse.Signature = append(currentResponse.Signature, response.Signature...)
				currentResponse.SignedText = append(currentResponse.SignedText, response.SignedText...)

				return result
			})
			testhelper.RequireGrpcError(t, setup.expectedErr, err)

			require.Len(t, actualResponses, len(setup.expectedResponses))
			for i, expected := range setup.expectedResponses {
				// We cannot use `testhelper.ProtoEqual` here due to it being too inefficient with
				// the data we're comparing because it contains multiple MB of signed data. This has
				// in the past led to frequent timeouts in CI.
				require.Equal(t, expected.CommitId, actualResponses[i].CommitId)
				require.Equal(t, string(expected.Signature), string(actualResponses[i].Signature))
				require.Equal(t, string(expected.SignedText), string(actualResponses[i].SignedText))
				require.Equal(t, string(expected.Signer), string(actualResponses[i].Signer))
			}
		})
	}
}

func createCommitWithSignature(t *testing.T, cfg config.Cfg, repoPath, signatureField, signature, commitMessage string) (git.ObjectID, string) {
	// Each line of the signature needs to start with a space so that Git recognizes it as a continuation of the
	// field.
	signatureLines := strings.Split(signature, "\n")
	for i, signatureLine := range signatureLines {
		signatureLines[i] = " " + signatureLine
	}

	commitData := fmt.Sprintf(`tree %s
author Bug Fixer <bugfixer@email.com> 1584564725 +0100
committer Bug Fixer <bugfixer@email.com> 1584564725 +0100

%s
`, gittest.DefaultObjectHash.EmptyTreeOID, commitMessage)

	signedCommitData := strings.Replace(commitData, "\n\n", fmt.Sprintf("\n%s%s\n\n", signatureField, strings.Join(signatureLines, "\n")), 1)

	stdout := gittest.ExecOpts(t, cfg, gittest.ExecConfig{
		Stdin: strings.NewReader(signedCommitData),
	}, "-C", repoPath, "hash-object", "-w", "-t", "commit", "--stdin", "--literally")

	commitID, err := gittest.DefaultObjectHash.FromHex(text.ChompBytes(stdout))
	require.NoError(t, err)

	return commitID, commitData
}
