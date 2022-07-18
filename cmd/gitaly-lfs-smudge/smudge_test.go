//go:build !gitaly_test_sha256

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/smudge"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

const (
	lfsOid     = "3ea5dd307f195f449f0e08234183b82e92c3d5f4cff11c2a6bb014f9e0de12aa"
	lfsPointer = `version https://git-lfs.github.com/spec/v1
oid sha256:3ea5dd307f195f449f0e08234183b82e92c3d5f4cff11c2a6bb014f9e0de12aa
size 177735
`
	lfsPointerWithCRLF = `version https://git-lfs.github.com/spec/v1
oid sha256:3ea5dd307f195f449f0e08234183b82e92c3d5f4cff11c2a6bb014f9e0de12aa` + "\r\nsize 177735"
	invalidLfsPointer = `version https://git-lfs.github.com/spec/v1
oid sha256:3ea5dd307f195f449f0e08234183b82e92c3d5f4cff11c2a6bb014f9e0de12aa&gl_repository=project-51
size 177735
`
	invalidLfsPointerWithNonHex = `version https://git-lfs.github.com/spec/v1
oid sha256:3ea5dd307f195f449f0e08234183b82e92c3d5f4cff11c2a6bb014f9e0de12z-
size 177735`
	glRepository = "project-1"
	secretToken  = "topsecret"
	testData     = "hello world"
	certPath     = "../../internal/gitlab/testdata/certs/server.crt"
	keyPath      = "../../internal/gitlab/testdata/certs/server.key"
)

var defaultOptions = gitlab.TestServerOptions{
	SecretToken:      secretToken,
	LfsBody:          testData,
	LfsOid:           lfsOid,
	GlRepository:     glRepository,
	ClientCACertPath: certPath,
	ServerCertPath:   certPath,
	ServerKeyPath:    keyPath,
}

func TestFilter_successful(t *testing.T) {
	testCases := []struct {
		desc string
		data string
	}{
		{
			desc: "regular LFS pointer",
			data: lfsPointer,
		},
		{
			desc: "LFS pointer with CRLF",
			data: lfsPointerWithCRLF,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			var b bytes.Buffer
			reader := strings.NewReader(tc.data)

			gitlabCfg, cleanup := runTestServer(t, defaultOptions)
			defer cleanup()

			cfg := smudge.Config{
				GlRepository: "project-1",
				Gitlab:       gitlabCfg,
				TLS: config.TLS{
					CertPath: certPath,
					KeyPath:  keyPath,
				},
			}

			require.NoError(t, filter(ctx, cfg, &b, reader))
			require.Equal(t, testData, b.String())
		})
	}
}

func TestFilter_unsuccessful(t *testing.T) {
	defaultConfig := func(t *testing.T, gitlabCfg config.Gitlab) smudge.Config {
		return smudge.Config{
			GlRepository: "project-1",
			Gitlab:       gitlabCfg,
		}
	}

	testCases := []struct {
		desc              string
		setupCfg          func(*testing.T, config.Gitlab) smudge.Config
		data              string
		expectedError     bool
		options           gitlab.TestServerOptions
		expectedGitalyTLS string
	}{
		{
			desc:          "bad LFS pointer",
			data:          "test data",
			setupCfg:      defaultConfig,
			options:       defaultOptions,
			expectedError: false,
		},
		{
			desc:          "invalid LFS pointer",
			data:          invalidLfsPointer,
			setupCfg:      defaultConfig,
			options:       defaultOptions,
			expectedError: false,
		},
		{
			desc:          "invalid LFS pointer with non-hex characters",
			data:          invalidLfsPointerWithNonHex,
			setupCfg:      defaultConfig,
			options:       defaultOptions,
			expectedError: false,
		},
		{
			desc: "missing GL_REPOSITORY",
			data: lfsPointer,
			setupCfg: func(t *testing.T, gitlabCfg config.Gitlab) smudge.Config {
				cfg := defaultConfig(t, gitlabCfg)
				cfg.GlRepository = ""
				return cfg
			},
			options:       defaultOptions,
			expectedError: true,
		},
		{
			desc: "missing GL_INTERNAL_CONFIG",
			data: lfsPointer,
			setupCfg: func(t *testing.T, gitlabCfg config.Gitlab) smudge.Config {
				cfg := defaultConfig(t, gitlabCfg)
				cfg.Gitlab = config.Gitlab{}
				return cfg
			},
			options:       defaultOptions,
			expectedError: true,
		},
		{
			desc:     "failed HTTP response",
			data:     lfsPointer,
			setupCfg: defaultConfig,
			options: gitlab.TestServerOptions{
				SecretToken:   secretToken,
				LfsBody:       testData,
				LfsOid:        lfsOid,
				GlRepository:  glRepository,
				LfsStatusCode: http.StatusInternalServerError,
			},
			expectedError: true,
		},
		{
			desc: "invalid TLS paths",
			data: lfsPointer,
			setupCfg: func(t *testing.T, gitlabCfg config.Gitlab) smudge.Config {
				cfg := defaultConfig(t, gitlabCfg)
				cfg.TLS = config.TLS{CertPath: "fake-path", KeyPath: "not-real"}
				return cfg
			},
			options:       defaultOptions,
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			gitlabCfg, cleanup := runTestServer(t, tc.options)
			defer cleanup()

			cfg := tc.setupCfg(t, gitlabCfg)

			var b bytes.Buffer
			err := filter(ctx, cfg, &b, strings.NewReader(tc.data))

			if tc.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.data, b.String())
			}
		})
	}
}

func TestProcess(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	gitlabCfg, cleanup := runTestServer(t, defaultOptions)
	defer cleanup()

	defaultSmudgeCfg := smudge.Config{
		GlRepository: "project-1",
		Gitlab:       gitlabCfg,
		TLS: config.TLS{
			CertPath: certPath,
			KeyPath:  keyPath,
		},
		DriverType: smudge.DriverTypeProcess,
	}

	pkt := func(data string) string {
		return fmt.Sprintf("%04x%s", len(data)+4, data)
	}
	flush := "0000"

	for _, tc := range []struct {
		desc           string
		cfg            smudge.Config
		input          []string
		expectedErr    error
		expectedOutput string
	}{
		{
			desc: "unsupported client",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-foobar-client\n"),
				pkt("version=2\n"),
				flush,
			},
			expectedErr: fmt.Errorf("invalid client %q", "git-foobar-client\n"),
		},
		{
			desc: "unsupported version",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=3\n"),
				flush,
			},
			expectedErr: fmt.Errorf("client does not support version 2"),
		},
		{
			desc: "unsupported capability",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=foobar\n"),
				flush,
			},
			expectedErr: fmt.Errorf("client does not support smudge capability"),
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
			}, ""),
		},
		{
			desc: "unsupported command",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("command=clean\n"),
				flush,
			},
			expectedErr: fmt.Errorf("expected smudge command, got %q", "command=clean\n"),
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
			}, ""),
		},
		{
			desc: "single non-LFS blob",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("command=smudge\n"),
				pkt("some=metadata\n"),
				pkt("more=metadata\n"),
				flush,
				pkt("something"),
				flush,
			},
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("status=success\n"),
				flush,
				pkt("something"),
				flush,
				flush,
			}, ""),
		},
		{
			desc: "single non-LFS blob with multiline contents",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("command=smudge\n"),
				pkt("some=metadata\n"),
				pkt("more=metadata\n"),
				flush,
				pkt("some"),
				pkt("thing"),
				flush,
			},
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("status=success\n"),
				flush,
				pkt("something"),
				flush,
				flush,
			}, ""),
		},
		{
			desc: "multiple non-LFS blobs",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("command=smudge\n"),
				flush,
				pkt("first"),
				flush,
				pkt("command=smudge\n"),
				flush,
				pkt("second"),
				flush,
			},
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("status=success\n"),
				flush,
				pkt("first"),
				flush,
				flush,
				pkt("status=success\n"),
				flush,
				pkt("second"),
				flush,
				flush,
			}, ""),
		},
		{
			desc: "single LFS blob",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("command=smudge\n"),
				flush,
				pkt(lfsPointer),
				flush,
			},
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("status=success\n"),
				flush,
				pkt("hello world"),
				flush,
				flush,
			}, ""),
		},
		{
			desc: "multiple LFS blobs",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("command=smudge\n"),
				flush,
				pkt(lfsPointer),
				flush,
				pkt("command=smudge\n"),
				flush,
				pkt(lfsPointer),
				flush,
			},
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("status=success\n"),
				flush,
				pkt("hello world"),
				flush,
				flush,
				pkt("status=success\n"),
				flush,
				pkt("hello world"),
				flush,
				flush,
			}, ""),
		},
		{
			desc: "full-blown session",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=1\n"),
				pkt("version=2\n"),
				pkt("version=99\n"),
				flush,
				pkt("capability=frobnicate\n"),
				pkt("capability=smudge\n"),
				pkt("capability=clean\n"),
				flush,
				// First blob.
				pkt("command=smudge\n"),
				pkt("unused=metadata\n"),
				flush,
				pkt("something something"),
				flush,
				// Second blob.
				pkt("command=smudge\n"),
				pkt("more=unused=metadata\n"),
				flush,
				pkt(lfsPointer),
				flush,
				// Third blob.
				pkt("command=smudge\n"),
				pkt("this=is a huge binary blob\n"),
				flush,
				pkt(strings.Repeat("1", pktline.MaxPktSize-4)),
				pkt(strings.Repeat("2", pktline.MaxPktSize-4)),
				pkt(strings.Repeat("3", pktline.MaxPktSize-4)),
				flush,
			},
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("status=success\n"),
				flush,
				pkt("something something"),
				flush,
				flush,
				pkt("status=success\n"),
				flush,
				pkt("hello world"),
				flush,
				flush,
				pkt("status=success\n"),
				flush,
				// This looks a bit funny, but can be attributed to the fact that we
				// use an io.Multireader to read the first 1024 bytes when parsing
				// the LFS pointer.
				pkt(strings.Repeat("1", 1024)),
				pkt(strings.Repeat("1", pktline.MaxPktSize-4-1024) + strings.Repeat("2", 1024)),
				pkt(strings.Repeat("2", pktline.MaxPktSize-4-1024) + strings.Repeat("3", 1024)),
				pkt(strings.Repeat("3", pktline.MaxPktSize-4-1024)),
				flush,
				flush,
			}, ""),
		},
		{
			desc: "partial failure",
			cfg:  defaultSmudgeCfg,
			input: []string{
				pkt("git-filter-client\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				// The first command sends an unknown LFS pointer that should cause
				// an error.
				pkt("command=smudge\n"),
				flush,
				pkt(strings.Join([]string{
					"version https://git-lfs.github.com/spec/v1",
					"oid sha256:1111111111111111111111111111111111111111111111111111111111111111",
					"size 177735",
				}, "\n") + "\n"),
				flush,
				// And the second command sends a known LFS pointer that should
				// still be processed as expected, regardless of the initial error.
				pkt("command=smudge\n"),
				flush,
				pkt(lfsPointer),
				flush,
			},
			expectedOutput: strings.Join([]string{
				pkt("git-filter-server\n"),
				pkt("version=2\n"),
				flush,
				pkt("capability=smudge\n"),
				flush,
				pkt("status=error\n"),
				flush,
				pkt("status=success\n"),
				flush,
				pkt("hello world"),
				flush,
				flush,
			}, ""),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			var inputBuffer bytes.Buffer
			for _, input := range tc.input {
				_, err := inputBuffer.WriteString(input)
				require.NoError(t, err)
			}

			var outputBuffer bytes.Buffer
			require.Equal(t, tc.expectedErr, process(ctx, tc.cfg, &outputBuffer, &inputBuffer))
			require.Equal(t, tc.expectedOutput, outputBuffer.String())
		})
	}
}
