package catfile

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestParseCommits(t *testing.T) {
	t.Parallel()

	type setupData struct {
		content        string
		oid            git.ObjectID
		expectedCommit *Commit
		expectedErr    error
	}

	// Valid-but-interesting commits should be test at the FindCommit level.
	// Invalid objects (that Git would complain about during fsck) can be
	// tested here.
	//
	// Once a repository contains a pathological object it can be hard to get
	// rid of it. Because of this I think it's nicer to ignore such objects
	// than to throw hard errors.
	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "empty commit object",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit:     &gitalypb.GitCommit{Id: gittest.DefaultObjectHash.EmptyTreeOID.String()},
						SignatureData: SignatureData{Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "no email",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "author Jane Doe",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id:     gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe")},
						},
						SignatureData: SignatureData{Payload: []byte("author Jane Doe"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "normal author",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "author Au Thor <au.thor@example.com> 1625121079 +0000",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Au Thor"),
								Email:    []byte("au.thor@example.com"),
								Date:     timestamppb.New(time.Unix(1625121079, 0)),
								Timezone: []byte("+0000"),
							},
						},
						SignatureData: SignatureData{Payload: []byte("author Au Thor <au.thor@example.com> 1625121079 +0000"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "author with missing mail",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "author Au Thor <> 1625121079 +0000",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Au Thor"),
								Date:     timestamppb.New(time.Unix(1625121079, 0)),
								Timezone: []byte("+0000"),
							},
						},
						SignatureData: SignatureData{Payload: []byte("author Au Thor <> 1625121079 +0000"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "author with missing date",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "author Au Thor <au.thor@example.com>",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:  []byte("Au Thor"),
								Email: []byte("au.thor@example.com"),
							},
						},
						SignatureData: SignatureData{Payload: []byte("author Au Thor <au.thor@example.com>"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "unmatched <",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "author Jane Doe <janedoe@example.com",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id:     gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe")},
						},
						SignatureData: SignatureData{Payload: []byte("author Jane Doe <janedoe@example.com"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "unmatched >",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "author Jane Doe janedoe@example.com>",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id:     gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe janedoe@example.com>")},
						},
						SignatureData: SignatureData{Payload: []byte("author Jane Doe janedoe@example.com>"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "date too high",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "author Jane Doe <janedoe@example.com> 9007199254740993 +0200",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Jane Doe"),
								Email:    []byte("janedoe@example.com"),
								Date:     &timestamppb.Timestamp{Seconds: 9223371974719179007},
								Timezone: []byte("+0200"),
							},
						},
						SignatureData: SignatureData{Payload: []byte("author Jane Doe <janedoe@example.com> 9007199254740993 +0200"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "date negative",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "author Jane Doe <janedoe@example.com> -1 +0200",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Jane Doe"),
								Email:    []byte("janedoe@example.com"),
								Date:     &timestamppb.Timestamp{Seconds: 9223371974719179007},
								Timezone: []byte("+0200"),
							},
						},
						SignatureData: SignatureData{Payload: []byte("author Jane Doe <janedoe@example.com> -1 +0200"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "huge",
			setup: func(_ *testing.T) setupData {
				repeat := strings.Repeat("A", 100000)
				content := "author " + repeat

				return setupData{
					content: content,
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name: []byte(repeat),
							},
						},
						SignatureData: SignatureData{Payload: []byte(content), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "has encoding",
			setup: func(_ *testing.T) setupData {
				return setupData{
					content: "encoding Windows-1251",
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id:       gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Encoding: "Windows-1251",
						},
						SignatureData: SignatureData{Payload: []byte("encoding Windows-1251"), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "PGP signed commit",
			setup: func(_ *testing.T) setupData {
				commitData, signedCommitData := createSignedCommitData(gpgSignaturePrefix, pgpSignature, "random commit message")

				return setupData{
					content: signedCommitData,
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							Committer: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							SignatureType: gitalypb.SignatureType_PGP,
							TreeId:        gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Subject:       []byte("random commit message"),
							Body:          []byte("random commit message\n"),
							BodySize:      22,
						},
						SignatureData: SignatureData{Payload: []byte(commitData), Signatures: [][]byte{[]byte(pgpSignature)}},
					},
				}
			},
		},
		{
			desc: "PGP SHA256-signed signed commit",
			setup: func(_ *testing.T) setupData {
				commitData, signedCommitData := createSignedCommitData(gpgSignaturePrefixSha256, pgpSignature, "random commit message")

				return setupData{
					content: signedCommitData,
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							Committer: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							SignatureType: gitalypb.SignatureType_PGP,
							TreeId:        gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Subject:       []byte("random commit message"),
							Body:          []byte("random commit message\n"),
							BodySize:      22,
						},
						SignatureData: SignatureData{Payload: []byte(commitData), Signatures: [][]byte{[]byte(pgpSignature)}},
					},
				}
			},
		},
		{
			desc: "SSH signed commit",
			setup: func(_ *testing.T) setupData {
				commitData, signedCommitData := createSignedCommitData(gpgSignaturePrefix, sshSignature, "random commit message")

				return setupData{
					content: signedCommitData,
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							Committer: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							SignatureType: gitalypb.SignatureType_SSH,
							TreeId:        gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Subject:       []byte("random commit message"),
							Body:          []byte("random commit message\n"),
							BodySize:      22,
						},
						SignatureData: SignatureData{Payload: []byte(commitData), Signatures: [][]byte{[]byte(sshSignature)}},
					},
				}
			},
		},
		{
			desc: "garbage signed commit",
			setup: func(_ *testing.T) setupData {
				_, signedCommitData := createSignedCommitData("gpgsig-garbage", sshSignature, "garbage-signed commit message")

				return setupData{
					content: signedCommitData,
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							Committer: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							TreeId:   gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Subject:  []byte("garbage-signed commit message"),
							Body:     []byte("garbage-signed commit message\n"),
							BodySize: 30,
						},
						SignatureData: SignatureData{Payload: []byte(signedCommitData), Signatures: [][]byte{}},
					},
				}
			},
		},
		{
			desc: "commits with multiple signatures",
			setup: func(_ *testing.T) setupData {
				commitData, signedCommitData := createSignedCommitData("gpgsig", pgpSignature, "commit message")

				signatureLines := strings.Split(sshSignature, "\n")
				for i, signatureLine := range signatureLines {
					signatureLines[i] = " " + signatureLine
				}

				signedCommitData = strings.Replace(signedCommitData, "\n\n", fmt.Sprintf("\n%s%s\n\n", "gpgsig-sha256", strings.Join(signatureLines, "\n")), 1)

				return setupData{
					content: signedCommitData,
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							Committer: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							SignatureType: gitalypb.SignatureType_PGP,
							TreeId:        gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Subject:       []byte("commit message"),
							Body:          []byte("commit message\n"),
							BodySize:      15,
						},
						SignatureData: SignatureData{Payload: []byte(commitData), Signatures: [][]byte{[]byte(pgpSignature), []byte(sshSignature)}},
					},
				}
			},
		},
		{
			desc: "PGP signed commit with headers after the signature",
			setup: func(_ *testing.T) setupData {
				commitMessage := "random commit message"
				commitData, signedCommitData := createSignedCommitData(gpgSignaturePrefix, pgpSignature, "random commit message")

				// Headers after the signature shouldn't be ignored and should be
				// part of the commit data. If ignored, malicious users can draft a
				// commit similar to a previously signed commit but with additional
				// headers and the new commit would still show as verified since we
				// won't add the additional headers to commit data.
				postSignatureHeader := fmt.Sprintf("parent %s", gittest.DefaultObjectHash.EmptyTreeOID)
				modifyCommitData := func(data string) string {
					return strings.Replace(
						data,
						fmt.Sprintf("\n%s", commitMessage),
						fmt.Sprintf("%s\n\n%s", postSignatureHeader, commitMessage),
						1,
					)
				}

				// We expect that these post signature headers are added to the commit data,
				// that way any modified commit will fail verification.
				signedCommitData = modifyCommitData(signedCommitData)
				commitData = modifyCommitData(commitData)

				return setupData{
					content: signedCommitData,
					oid:     gittest.DefaultObjectHash.EmptyTreeOID,
					expectedCommit: &Commit{
						GitCommit: &gitalypb.GitCommit{
							Id: gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Author: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							Committer: &gitalypb.CommitAuthor{
								Name:     []byte("Bug Fixer"),
								Email:    []byte("bugfixer@email.com"),
								Date:     &timestamppb.Timestamp{Seconds: 1584564725},
								Timezone: []byte("+0100"),
							},
							SignatureType: gitalypb.SignatureType_PGP,
							TreeId:        gittest.DefaultObjectHash.EmptyTreeOID.String(),
							Subject:       []byte("random commit message"),
							Body:          []byte("random commit message\n"),
							BodySize:      22,
						},
						SignatureData: SignatureData{Payload: []byte(commitData), Signatures: [][]byte{[]byte(pgpSignature)}},
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			commit, err := newParser().parseCommit(newStaticObject(setup.content, "commit", setup.oid))
			require.Equal(t, setup.expectedErr, err)
			testhelper.ProtoEqual(t, setup.expectedCommit.GitCommit, commit.GitCommit)
			require.Equal(t, setup.expectedCommit.SignatureData.Payload, commit.SignatureData.Payload)
			require.Equal(t, setup.expectedCommit.SignatureData.Signatures, commit.SignatureData.Signatures)
		})
	}
}

func createSignedCommitData(signatureField, signature, commitMessage string) (string, string) {
	commitData := fmt.Sprintf(`tree %s
author Bug Fixer <bugfixer@email.com> 1584564725 +0100
committer Bug Fixer <bugfixer@email.com> 1584564725 +0100

%s
`, gittest.DefaultObjectHash.EmptyTreeOID, commitMessage)

	// Each line of the signature needs to start with a space so that Git recognizes it as a continuation of the
	// field.
	signatureLines := strings.Split(signature, "\n")
	for i, signatureLine := range signatureLines {
		signatureLines[i] = " " + signatureLine
	}

	signedCommitData := strings.Replace(commitData, "\n\n", fmt.Sprintf("\n%s%s\n\n", signatureField, strings.Join(signatureLines, "\n")), 1)

	return commitData, signedCommitData
}
