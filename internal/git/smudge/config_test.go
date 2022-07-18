//go:build !gitaly_test_sha256

package smudge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
)

func TestConfigFromEnvironment(t *testing.T) {
	gitlabCfg := config.Gitlab{
		URL:             "https://example.com",
		RelativeURLRoot: "gitlab",
		HTTPSettings: config.HTTPSettings{
			ReadTimeout: 1,
			User:        "user",
			Password:    "correcthorsebatterystaple",
			CAFile:      "/ca/file",
			CAPath:      "/ca/path",
		},
		SecretFile: "/secret/path",
	}

	tlsCfg := config.TLS{
		CertPath: "/cert/path",
		KeyPath:  "/key/path",
	}

	fullCfg := Config{
		GlRepository: "repo",
		Gitlab:       gitlabCfg,
		TLS:          tlsCfg,
	}

	marshalledGitlabCfg, err := json.Marshal(gitlabCfg)
	require.NoError(t, err)

	marshalledTLSCfg, err := json.Marshal(tlsCfg)
	require.NoError(t, err)

	marshalledFullCfg, err := json.Marshal(fullCfg)
	require.NoError(t, err)

	for _, tc := range []struct {
		desc        string
		environment []string
		expectedErr string
		expectedCfg Config
	}{
		{
			desc:        "empty environment",
			expectedErr: "error loading project: GL_REPOSITORY is not defined",
		},
		{
			desc: "successful via separate envvars",
			environment: []string{
				"GL_REPOSITORY=repo",
				"GL_INTERNAL_CONFIG=" + string(marshalledGitlabCfg),
				"GITALY_TLS=" + string(marshalledTLSCfg),
			},
			expectedCfg: Config{
				GlRepository: "repo",
				Gitlab:       gitlabCfg,
				TLS:          tlsCfg,
			},
		},
		{
			desc: "successful via single envvar",
			environment: []string{
				"GITALY_LFS_SMUDGE_CONFIG=" + string(marshalledFullCfg),
				"GL_REPOSITORY=garbage",
				"GL_INTERNAL_CONFIG=garbage",
				"GITALY_TLS=garbage",
			},
			expectedCfg: fullCfg,
		},
		{
			desc: "single envvar overrides separate envvars",
			environment: []string{
				"GITALY_LFS_SMUDGE_CONFIG=" + string(marshalledFullCfg),
			},
			expectedCfg: fullCfg,
		},
		{
			desc: "invalid full config",
			environment: []string{
				"GITALY_LFS_SMUDGE_CONFIG=invalid",
			},
			expectedErr: "unable to unmarshal config: invalid character 'i' looking for beginning of value",
		},
		{
			desc: "missing GlRepository",
			environment: []string{
				"GL_INTERNAL_CONFIG=" + string(marshalledGitlabCfg),
				"GITALY_TLS=" + string(marshalledTLSCfg),
			},
			expectedErr: "error loading project: GL_REPOSITORY is not defined",
		},
		{
			desc: "missing Gitlab config",
			environment: []string{
				"GL_REPOSITORY=repo",
				"GITALY_TLS=" + string(marshalledTLSCfg),
			},
			expectedErr: "unable to retrieve GL_INTERNAL_CONFIG",
		},
		{
			desc: "invalid Gitlab config",
			environment: []string{
				"GL_REPOSITORY=repo",
				"GL_INTERNAL_CONFIG=invalid",
				"GITALY_TLS=" + string(marshalledTLSCfg),
			},
			expectedErr: "unable to unmarshal GL_INTERNAL_CONFIG: invalid character 'i' looking for beginning of value",
		},
		{
			desc: "missing TLS config",
			environment: []string{
				"GL_REPOSITORY=repo",
				"GL_INTERNAL_CONFIG=" + string(marshalledGitlabCfg),
			},
			expectedErr: "unable to retrieve GITALY_TLS",
		},
		{
			desc: "invalid TLS config",
			environment: []string{
				"GL_REPOSITORY=repo",
				"GL_INTERNAL_CONFIG=" + string(marshalledGitlabCfg),
				"GITALY_TLS=invalid",
			},
			expectedErr: "unable to unmarshal GITALY_TLS: invalid character 'i' looking for beginning of value",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, err := ConfigFromEnvironment(tc.environment)
			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedErr)
			}
			require.Equal(t, tc.expectedCfg, cfg)
		})
	}
}

func TestConfig_Environment(t *testing.T) {
	cfg := Config{
		GlRepository: "repo",
		Gitlab: config.Gitlab{
			URL:             "https://example.com",
			RelativeURLRoot: "gitlab",
			HTTPSettings: config.HTTPSettings{
				ReadTimeout: 1,
				User:        "user",
				Password:    "correcthorsebatterystaple",
				CAFile:      "/ca/file",
				CAPath:      "/ca/path",
			},
			SecretFile: "/secret/path",
		},
		TLS: config.TLS{
			CertPath: "/cert/path",
			KeyPath:  "/key/path",
		},
	}

	env, err := cfg.Environment()
	require.NoError(t, err)
	require.Equal(t, `GITALY_LFS_SMUDGE_CONFIG={"gl_repository":"repo","gitlab":{"url":"https://example.com","relative_url_root":"gitlab","http_settings":{"read_timeout":1,"user":"user","password":"correcthorsebatterystaple","ca_file":"/ca/file","ca_path":"/ca/path"},"secret_file":"/secret/path"},"tls":{"cert_path":"/cert/path","key_path":"/key/path"},"driver_type":0}`, env)
}
