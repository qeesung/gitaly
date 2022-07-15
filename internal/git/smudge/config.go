package smudge

import (
	"encoding/json"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
)

// ConfigEnvironmentKey is the key that gitaly-lfs-smudge expects the configuration to exist at. The
// value of this environment variable should be the JSON-encoded `Config` struct.
const ConfigEnvironmentKey = "GITALY_LFS_SMUDGE_CONFIG"

// DriverType determines the type of the smudge filter.
type DriverType int

const (
	// DriverTypeFilter indicates that the smudge filter is to be run once per object. This is
	// the current default but will be phased out eventually in favor of DriverTypeProcess.
	DriverTypeFilter = DriverType(0)
	// DriverTypeProcess is a long-running smudge filter that can process multiple objects in
	// one session. See gitattributes(5), "Long Running Filter Process".
	DriverTypeProcess = DriverType(1)
)

// Config is the configuration used to run gitaly-lfs-smudge. It must be injected via environment
// variables.
type Config struct {
	// GlRepository is the GitLab repository identifier that is required so that we can query
	// the corresponding Rails' repository for the respective LFS contents.
	GlRepository string `json:"gl_repository"`
	// Gitlab contains configuration so that we can connect to Rails in order to retrieve LFS
	// contents.
	Gitlab config.Gitlab `json:"gitlab"`
	// TLS contains configuration for setting up a TLS-encrypted connection to Rails.
	TLS config.TLS `json:"tls"`
	// DriverType is the type of the smudge driver that should be used.
	DriverType DriverType `json:"driver_type"`
}

// ConfigFromEnvironment loads the Config structure from the set of given environment variables.
func ConfigFromEnvironment(environment []string) (Config, error) {
	var cfg Config

	// If ConfigEnvironmentKey is set, then we use that instead of the separate environment
	// variables queried for below. This has been newly introduced in v15.1, so the fallback
	// to the old environment variables can be removed with v15.2.
	if encodedCfg := env.ExtractValue(environment, ConfigEnvironmentKey); encodedCfg != "" {
		if err := json.Unmarshal([]byte(encodedCfg), &cfg); err != nil {
			return Config{}, fmt.Errorf("unable to unmarshal config: %w", err)
		}

		return cfg, nil
	}

	cfg.GlRepository = env.ExtractValue(environment, "GL_REPOSITORY")
	if cfg.GlRepository == "" {
		return Config{}, fmt.Errorf("error loading project: GL_REPOSITORY is not defined")
	}

	u := env.ExtractValue(environment, "GL_INTERNAL_CONFIG")
	if u == "" {
		return Config{}, fmt.Errorf("unable to retrieve GL_INTERNAL_CONFIG")
	}

	if err := json.Unmarshal([]byte(u), &cfg.Gitlab); err != nil {
		return Config{}, fmt.Errorf("unable to unmarshal GL_INTERNAL_CONFIG: %w", err)
	}

	u = env.ExtractValue(environment, "GITALY_TLS")
	if u == "" {
		return Config{}, errors.New("unable to retrieve GITALY_TLS")
	}

	if err := json.Unmarshal([]byte(u), &cfg.TLS); err != nil {
		return Config{}, fmt.Errorf("unable to unmarshal GITALY_TLS: %w", err)
	}

	return cfg, nil
}

// Environment encodes the given configuration as an environment variable that can be injected into
// `gitaly-lfs-smudge`.
func (c Config) Environment() (string, error) {
	marshalled, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshalling configuration: %w", err)
	}

	return fmt.Sprintf("%s=%s", ConfigEnvironmentKey, marshalled), nil
}

// GitConfiguration returns the Git configuration required to run the smudge filter.
func (c Config) GitConfiguration(cfg config.Cfg) (git.ConfigPair, error) {
	switch c.DriverType {
	case DriverTypeFilter:
		return git.ConfigPair{
			Key:   "filter.lfs.smudge",
			Value: cfg.BinaryPath("gitaly-lfs-smudge"),
		}, nil
	case DriverTypeProcess:
		return git.ConfigPair{
			Key:   "filter.lfs.process",
			Value: cfg.BinaryPath("gitaly-lfs-smudge"),
		}, nil
	default:
		return git.ConfigPair{}, fmt.Errorf("unknown driver type: %v", c.DriverType)
	}
}
