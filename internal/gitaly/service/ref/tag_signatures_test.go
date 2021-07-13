package ref

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testassert"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetTagSignatures(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	cfg, repoProto, repoPath, client := setupRefService(t)

	message1 := strings.Repeat("a", helper.MaxCommitOrTagMessageSize) + "\n"
	signature1 := "-----BEGIN PGP SIGNATURE-----\niJwEAAEIAAYFAlmmbf0ACgkQv52SX5Ee/WVv1gP/WrjclOc3CYiTrTgNuxs/vyXl\nPtUrxbOYEpQtRV+id/agJgaJWKoHRYUoXSGBuykijsND43PMkrjfZiF2SbE0j0CS\n7oukSx83us+y/hn+ecZQG0OkrBZ2vQ9gHGbEN9RFyZXfgWtRcgqFnFZUxCznhzzk\nnjAjb08aUpn9r8WHsdU=\n=HLte\n-----END PGP SIGNATURE-----"
	tag1ID := gittest.CreateTag(t, cfg, repoPath, "big-tag-1", "master", &gittest.CreateTagOpts{Message: message1 + signature1})
	signedText1 := "object 1e292f8fedd741b75372e19097c76d327140c312\ntype commit\ntag big-tag-1\ntagger Scrooge McDuck <scrooge@mcduck.com> 1572776879 +0100\n\n" + message1

	message2 := strings.Repeat("b", helper.MaxCommitOrTagMessageSize) + "\n"
	signature2 := "-----BEGIN SIGNED MESSAGE-----\nMIISfwYJKoZIhvcNAQcCoIIScDCCEmwCAQExDTALBglghkgBZQMEAgEwCwYJKoZI\nhvcNAQcBoIIP8zCCB3QwggVcoAMCAQICBBXXLOIwDQYJKoZIhvcNAQELBQAwgbYx\nCzAJBgNVBAYTAkRFMQ8wDQYDVQQIDAZCYXllcm4xETAPBgNVBAcMCE11ZW5jaGVu\nMRAwDgYDVQQKDAdTaWVtZW5zMREwDwYDVQQFEwhaWlpaWlpBNjEdMBsGA1UECwwU\nU2llbWVucyBUcnVzdCBDZW50ZXIxPzA9BgNVBAMMNlNpZW1lbnMgSXNzdWluZyBD\nQSBNZWRpdW0gU3RyZW5ndGggQXV0aGVudGljYXRpb24gMjAxNjAeFw0xNzAyMDMw\nNjU4MzNaFw0yMDAyMDMwNjU4MzNaMFsxETAPBgNVBAUTCFowMDBOV0RIMQ4wDAYD\nVQQqDAVSb2dlcjEOMAwGA1UEBAwFTWVpZXIxEDAOBgNVBAoMB1NpZW1lbnMxFDAS\nBgNVBAMMC01laWVyIFJvZ2VyMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKC\nAQEAuBNea/68ZCnHYQjpm/k3ZBG0wBpEKSwG6lk9CEQlSxsqVLQHAoAKBIlJm1in\nYVLcK/Sq1yhYJ/qWcY/M53DhK2rpPuhtrWJUdOUy8EBWO20F4bd4Fw9pO7jt8bme\nu33TSrK772vKjuppzB6SeG13Cs08H+BIeD106G27h7ufsO00pvsxoSDL+uc4slnr\npBL+2TAL7nSFnB9QHWmRIK27SPqJE+lESdb0pse11x1wjvqKy2Q7EjL9fpqJdHzX\nNLKHXd2r024TOORTa05DFTNR+kQEKKV96XfpYdtSBomXNQ44cisiPBJjFtYvfnFE\nwgrHa8fogn/b0C+A+HAoICN12wIDAQABo4IC4jCCAt4wHQYDVR0OBBYEFCF+gkUp\nXQ6xGc0kRWXuDFxzA14zMEMGA1UdEQQ8MDqgIwYKKwYBBAGCNxQCA6AVDBNyLm1l\naWVyQHNpZW1lbnMuY29tgRNyLm1laWVyQHNpZW1lbnMuY29tMA4GA1UdDwEB/wQE\nAwIHgDAdBgNVHSUEFjAUBggrBgEFBQcDAgYIKwYBBQUHAwQwgcoGA1UdHwSBwjCB\nvzCBvKCBuaCBtoYmaHR0cDovL2NoLnNpZW1lbnMuY29tL3BraT9aWlpaWlpBNi5j\ncmyGQWxkYXA6Ly9jbC5zaWVtZW5zLm5ldC9DTj1aWlpaWlpBNixMPVBLST9jZXJ0\naWZpY2F0ZVJldm9jYXRpb25MaXN0hklsZGFwOi8vY2wuc2llbWVucy5jb20vQ049\nWlpaWlpaQTYsbz1UcnVzdGNlbnRlcj9jZXJ0aWZpY2F0ZVJldm9jYXRpb25MaXN0\nMEUGA1UdIAQ+MDwwOgYNKwYBBAGhaQcCAgMBAzApMCcGCCsGAQUFBwIBFhtodHRw\nOi8vd3d3LnNpZW1lbnMuY29tL3BraS8wDAYDVR0TAQH/BAIwADAfBgNVHSMEGDAW\ngBT4FV1HDGx3e3LEAheRaKK292oJRDCCAQQGCCsGAQUFBwEBBIH3MIH0MDIGCCsG\nAQUFBzAChiZodHRwOi8vYWguc2llbWVucy5jb20vcGtpP1paWlpaWkE2LmNydDBB\nBggrBgEFBQcwAoY1bGRhcDovL2FsLnNpZW1lbnMubmV0L0NOPVpaWlpaWkE2LEw9\nUEtJP2NBQ2VydGlmaWNhdGUwSQYIKwYBBQUHMAKGPWxkYXA6Ly9hbC5zaWVtZW5z\nLmNvbS9DTj1aWlpaWlpBNixvPVRydXN0Y2VudGVyP2NBQ2VydGlmaWNhdGUwMAYI\nKwYBBQUHMAGGJGh0dHA6Ly9vY3NwLnBraS1zZXJ2aWNlcy5zaWVtZW5zLmNvbTAN\nBgkqhkiG9w0BAQsFAAOCAgEAXPVcX6vaEcszJqg5IemF9aFTlwTrX5ITNIpzcqG+\nkD5haOf2mZYLjl+MKtLC1XfmIsGCUZNb8bjP6QHQEI+2d6x/ZOqPq7Kd7PwVu6x6\nxZrkDjUyhUbUntT5+RBy++l3Wf6Cq6Kx+K8ambHBP/bu90/p2U8KfFAG3Kr2gI2q\nfZrnNMOxmJfZ3/sXxssgLkhbZ7hRa+MpLfQ6uFsSiat3vlawBBvTyHnoZ/7oRc8y\nqi6QzWcd76CPpMElYWibl+hJzKbBZUWvc71AzHR6i1QeZ6wubYz7vr+FF5Y7tnxB\nVz6omPC9XAg0F+Dla6Zlz3Awj5imCzVXa+9SjtnsidmJdLcKzTAKyDewewoxYOOJ\nj3cJU7VSjJPl+2fVmDBaQwcNcUcu/TPAKApkegqO7tRF9IPhjhW8QkRnkqMetO3D\nOXmAFVIsEI0Hvb2cdb7B6jSpjGUuhaFm9TCKhQtCk2p8JCDTuaENLm1x34rrJKbT\n2vzyYN0CZtSkUdgD4yQxK9VWXGEzexRisWb4AnZjD2NAquLPpXmw8N0UwFD7MSpC\ndpaX7FktdvZmMXsnGiAdtLSbBgLVWOD1gmJFDjrhNbI8NOaOaNk4jrfGqNh5lhGU\n4DnBT2U6Cie1anLmFH/oZooAEXR2o3Nu+1mNDJChnJp0ovs08aa3zZvBdcloOvfU\nqdowggh3MIIGX6ADAgECAgQtyi/nMA0GCSqGSIb3DQEBCwUAMIGZMQswCQYDVQQG\nEwJERTEPMA0GA1UECAwGQmF5ZXJuMREwDwYDVQQHDAhNdWVuY2hlbjEQMA4GA1UE\nCgwHU2llbWVuczERMA8GA1UEBRMIWlpaWlpaQTExHTAbBgNVBAsMFFNpZW1lbnMg\nVHJ1c3QgQ2VudGVyMSIwIAYDVQQDDBlTaWVtZW5zIFJvb3QgQ0EgVjMuMCAyMDE2\nMB4XDTE2MDcyMDEzNDYxMFoXDTIyMDcyMDEzNDYxMFowgbYxCzAJBgNVBAYTAkRF\nMQ8wDQYDVQQIDAZCYXllcm4xETAPBgNVBAcMCE11ZW5jaGVuMRAwDgYDVQQKDAdT\naWVtZW5zMREwDwYDVQQFEwhaWlpaWlpBNjEdMBsGA1UECwwUU2llbWVucyBUcnVz\ndCBDZW50ZXIxPzA9BgNVBAMMNlNpZW1lbnMgSXNzdWluZyBDQSBNZWRpdW0gU3Ry\nZW5ndGggQXV0aGVudGljYXRpb24gMjAxNjCCAiIwDQYJKoZIhvcNAQEBBQADggIP\nADCCAgoCggIBAL9UfK+JAZEqVMVvECdYF9IK4KSw34AqyNl3rYP5x03dtmKaNu+2\n0fQqNESA1NGzw3s6LmrKLh1cR991nB2cvKOXu7AvEGpSuxzIcOROd4NpvRx+Ej1p\nJIPeqf+ScmVK7lMSO8QL/QzjHOpGV3is9sG+ZIxOW9U1ESooy4Hal6ZNs4DNItsz\npiCKqm6G3et4r2WqCy2RRuSqvnmMza7Y8BZsLy0ZVo5teObQ37E/FxqSrbDI8nxn\nB7nVUve5ZjrqoIGSkEOtyo11003dVO1vmWB9A0WQGDqE/q3w178hGhKfxzRaqzyi\nSoADUYS2sD/CglGTUxVq6u0pGLLsCFjItcCWqW+T9fPYfJ2CEd5b3hvqdCn+pXjZ\n/gdX1XAcdUF5lRnGWifaYpT9n4s4adzX8q6oHSJxTppuAwLRKH6eXALbGQ1I9lGQ\nDSOipD/09xkEsPw6HOepmf2U3YxZK1VU2sHqugFJboeLcHMzp6E1n2ctlNG1GKE9\nFDHmdyFzDi0Nnxtf/GgVjnHF68hByEE1MYdJ4nJLuxoT9hyjYdRW9MpeNNxxZnmz\nW3zh7QxIqP0ZfIz6XVhzrI9uZiqwwojDiM5tEOUkQ7XyW6grNXe75yt6mTj89LlB\nH5fOW2RNmCy/jzBXDjgyskgK7kuCvUYTuRv8ITXbBY5axFA+CpxZqokpAgMBAAGj\nggKmMIICojCCAQUGCCsGAQUFBwEBBIH4MIH1MEEGCCsGAQUFBzAChjVsZGFwOi8v\nYWwuc2llbWVucy5uZXQvQ049WlpaWlpaQTEsTD1QS0k/Y0FDZXJ0aWZpY2F0ZTAy\nBggrBgEFBQcwAoYmaHR0cDovL2FoLnNpZW1lbnMuY29tL3BraT9aWlpaWlpBMS5j\ncnQwSgYIKwYBBQUHMAKGPmxkYXA6Ly9hbC5zaWVtZW5zLmNvbS91aWQ9WlpaWlpa\nQTEsbz1UcnVzdGNlbnRlcj9jQUNlcnRpZmljYXRlMDAGCCsGAQUFBzABhiRodHRw\nOi8vb2NzcC5wa2ktc2VydmljZXMuc2llbWVucy5jb20wHwYDVR0jBBgwFoAUcG2g\nUOyp0CxnnRkV/v0EczXD4tQwEgYDVR0TAQH/BAgwBgEB/wIBADBABgNVHSAEOTA3\nMDUGCCsGAQQBoWkHMCkwJwYIKwYBBQUHAgEWG2h0dHA6Ly93d3cuc2llbWVucy5j\nb20vcGtpLzCBxwYDVR0fBIG/MIG8MIG5oIG2oIGzhj9sZGFwOi8vY2wuc2llbWVu\ncy5uZXQvQ049WlpaWlpaQTEsTD1QS0k/YXV0aG9yaXR5UmV2b2NhdGlvbkxpc3SG\nJmh0dHA6Ly9jaC5zaWVtZW5zLmNvbS9wa2k/WlpaWlpaQTEuY3JshkhsZGFwOi8v\nY2wuc2llbWVucy5jb20vdWlkPVpaWlpaWkExLG89VHJ1c3RjZW50ZXI/YXV0aG9y\naXR5UmV2b2NhdGlvbkxpc3QwJwYDVR0lBCAwHgYIKwYBBQUHAwIGCCsGAQUFBwME\nBggrBgEFBQcDCTAOBgNVHQ8BAf8EBAMCAQYwHQYDVR0OBBYEFPgVXUcMbHd7csQC\nF5Foorb3aglEMA0GCSqGSIb3DQEBCwUAA4ICAQBw+sqMp3SS7DVKcILEmXbdRAg3\nlLO1r457KY+YgCT9uX4VG5EdRKcGfWXK6VHGCi4Dos5eXFV34Mq/p8nu1sqMuoGP\nYjHn604eWDprhGy6GrTYdxzcE/GGHkpkuE3Ir/45UcmZlOU41SJ9SNjuIVrSHMOf\nccSY42BCspR/Q1Z/ykmIqQecdT3/Kkx02GzzSN2+HlW6cEO4GBW5RMqsvd2n0h2d\nfe2zcqOgkLtx7u2JCR/U77zfyxG3qXtcymoz0wgSHcsKIl+GUjITLkHfS9Op8V7C\nGr/dX437sIg5pVHmEAWadjkIzqdHux+EF94Z6kaHywohc1xG0KvPYPX7iSNjkvhz\n4NY53DHmxl4YEMLffZnaS/dqyhe1GTpcpyN8WiR4KuPfxrkVDOsuzWFtMSvNdlOV\ngdI0MXcLMP+EOeANZWX6lGgJ3vWyemo58nzgshKd24MY3w3i6masUkxJH2KvI7UH\n/1Db3SC8oOUjInvSRej6M3ZhYWgugm6gbpUgFoDw/o9Cg6Qm71hY0JtcaPC13rzm\nN8a2Br0+Fa5e2VhwLmAxyfe1JKzqPwuHT0S5u05SQghL5VdzqfA8FCL/j4XC9yI6\ncsZTAQi73xFQYVjZt3+aoSz84lOlTmVo/jgvGMY/JzH9I4mETGgAJRNj34Z/0meh\nM+pKWCojNH/dgyJSwDGCAlIwggJOAgEBMIG/MIG2MQswCQYDVQQGEwJERTEPMA0G\nA1UECAwGQmF5ZXJuMREwDwYDVQQHDAhNdWVuY2hlbjEQMA4GA1UECgwHU2llbWVu\nczERMA8GA1UEBRMIWlpaWlpaQTYxHTAbBgNVBAsMFFNpZW1lbnMgVHJ1c3QgQ2Vu\ndGVyMT8wPQYDVQQDDDZTaWVtZW5zIElzc3VpbmcgQ0EgTWVkaXVtIFN0cmVuZ3Ro\nIEF1dGhlbnRpY2F0aW9uIDIwMTYCBBXXLOIwCwYJYIZIAWUDBAIBoGkwHAYJKoZI\nhvcNAQkFMQ8XDTE5MTEyMDE0NTYyMFowLwYJKoZIhvcNAQkEMSIEIJDnZUpcVLzC\nOdtpkH8gtxwLPIDE0NmAmFC9uM8q2z+OMBgGCSqGSIb3DQEJAzELBgkqhkiG9w0B\nBwEwCwYJKoZIhvcNAQEBBIIBAH/Pqv2xp3a0jSPkwU1K3eGA/1lfoNJMUny4d/PS\nLVWlkgrmedXdLmuBzAGEaaZOJS0lEpNd01pR/reHs7xxZ+RZ0olTs2ufM0CijQSx\nOL9HDl2O3OoD77NWx4tl3Wy1yJCeV3XH/cEI7AkKHCmKY9QMoMYWh16ORBtr+YcS\nYK+gONOjpjgcgTJgZ3HSFgQ50xiD4WT1kFBHsuYsLqaOSbTfTN6Ayyg4edjrPQqa\nVcVf1OQcIrfWA3yMQrnEZfOYfN/D4EPjTfxBV+VCi/F2bdZmMbJ7jNk1FbewSwWO\nSDH1i0K32NyFbnh0BSos7njq7ELqKlYBsoB/sZfaH2vKy5U=\n-----END SIGNED MESSAGE-----"
	tag2ID := gittest.CreateTag(t, cfg, repoPath, "big-tag-2", "master~", &gittest.CreateTagOpts{Message: message2 + signature2})
	signedText2 := "object 7975be0116940bf2ad4321f79d02a55c5f7779aa\ntype commit\ntag big-tag-2\ntagger Scrooge McDuck <scrooge@mcduck.com> 1572776879 +0100\n\n" + message2

	message3 := "tag message\n"
	tag3ID := gittest.CreateTag(t, cfg, repoPath, "tag-3", "master~~", &gittest.CreateTagOpts{Message: message3})
	signedText3 := "object 60ecb67744cb56576c30214ff52294f8ce2def98\ntype commit\ntag tag-3\ntagger Scrooge McDuck <scrooge@mcduck.com> 1572776879 +0100\n\n" + message3

	for _, tc := range []struct {
		desc               string
		revisions          []string
		expectedErr        error
		expectedSignatures []*gitalypb.GetTagSignaturesResponse_TagSignature
	}{
		{
			desc:        "missing revisions",
			revisions:   []string{},
			expectedErr: status.Error(codes.InvalidArgument, "missing revisions"),
		},
		{
			desc: "invalid revision",
			revisions: []string{
				"--foobar",
			},
			expectedErr: status.Error(codes.InvalidArgument, "invalid revision: \"--foobar\""),
		},
		{
			desc: "unknown id",
			revisions: []string{
				"5937ac0a7beb003549fc5fd26fc247adbce4a52e",
			},
			expectedSignatures: nil,
		},
		{
			desc: "commit id",
			revisions: []string{
				"1e292f8fedd741b75372e19097c76d327140c312",
			},
			expectedSignatures: nil,
		},
		{
			desc: "commit ref",
			revisions: []string{
				"refs/heads/master",
			},
			expectedSignatures: nil,
		},
		{
			desc: "single tag signature",
			revisions: []string{
				tag1ID,
			},
			expectedSignatures: []*gitalypb.GetTagSignaturesResponse_TagSignature{
				{
					TagId:      tag1ID,
					Signature:  []byte(signature1),
					SignedText: []byte(signedText1),
				},
			},
		},
		{
			desc: "single tag signature by short SHA",
			revisions: []string{
				tag1ID[:7],
			},
			expectedSignatures: []*gitalypb.GetTagSignaturesResponse_TagSignature{
				{
					TagId:      tag1ID,
					Signature:  []byte(signature1),
					SignedText: []byte(signedText1),
				},
			},
		},
		{
			desc: "single tag signature by ref",
			revisions: []string{
				"refs/tags/big-tag-1",
			},
			expectedSignatures: []*gitalypb.GetTagSignaturesResponse_TagSignature{
				{
					TagId:      tag1ID,
					Signature:  []byte(signature1),
					SignedText: []byte(signedText1),
				},
			},
		},
		{
			desc: "multiple tag signatures",
			revisions: []string{
				tag1ID,
				tag2ID,
			},
			expectedSignatures: []*gitalypb.GetTagSignaturesResponse_TagSignature{
				{
					TagId:      tag1ID,
					Signature:  []byte(signature1),
					SignedText: []byte(signedText1),
				},
				{
					TagId:      tag2ID,
					Signature:  []byte(signature2),
					SignedText: []byte(signedText2),
				},
			},
		},
		{
			desc: "tag without signature",
			revisions: []string{
				tag3ID,
			},
			expectedSignatures: []*gitalypb.GetTagSignaturesResponse_TagSignature{
				{
					TagId:      tag3ID,
					Signature:  []byte(""),
					SignedText: []byte(signedText3),
				},
			},
		},
		{
			desc: "pseudorevisions",
			revisions: []string{
				"--not",
				"--all",
			},
			expectedSignatures: nil,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.GetTagSignatures(ctx, &gitalypb.GetTagSignaturesRequest{
				Repository:   repoProto,
				TagRevisions: tc.revisions,
			})
			require.NoError(t, err)

			var signatures []*gitalypb.GetTagSignaturesResponse_TagSignature
			for {
				resp, err := stream.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						testassert.GrpcEqualErr(t, tc.expectedErr, err)
					}
					break
				}

				signatures = append(signatures, resp.Signatures...)
			}

			testassert.ProtoEqual(t, tc.expectedSignatures, signatures)
		})
	}
}
