package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/smudge"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
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

func TestSuccessfulLfsSmudge(t *testing.T) {
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

			require.NoError(t, smudgeContents(cfg, &b, reader))
			require.Equal(t, testData, b.String())
		})
	}
}

func TestUnsuccessfulLfsSmudge(t *testing.T) {
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
			gitlabCfg, cleanup := runTestServer(t, tc.options)
			defer cleanup()

			cfg := tc.setupCfg(t, gitlabCfg)

			var b bytes.Buffer
			err := smudgeContents(cfg, &b, strings.NewReader(tc.data))

			if tc.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.data, b.String())
			}
		})
	}
}
