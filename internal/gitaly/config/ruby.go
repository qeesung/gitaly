package config

import (
	"fmt"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/duration"
)

// Ruby contains setting for Ruby worker processes
type Ruby struct {
	Dir                       string            `toml:"dir"`
	MaxRSS                    int               `toml:"max_rss"`
	GracefulRestartTimeout    duration.Duration `toml:"graceful_restart_timeout"`
	RestartDelay              duration.Duration `toml:"restart_delay"`
	NumWorkers                int               `toml:"num_workers"`
	LinguistLanguagesPath     string            `toml:"linguist_languages_path"`
	RuggedGitConfigSearchPath string            `toml:"rugged_git_config_search_path"`
}

// ConfigureRuby validates the gitaly-ruby configuration and sets default values.
func (cfg *Cfg) ConfigureRuby() error {
	if cfg.Ruby.GracefulRestartTimeout.Duration() == 0 {
		cfg.Ruby.GracefulRestartTimeout = duration.Duration(10 * time.Minute)
	}

	if cfg.Ruby.MaxRSS == 0 {
		cfg.Ruby.MaxRSS = 200 * 1024 * 1024
	}

	if cfg.Ruby.RestartDelay.Duration() == 0 {
		cfg.Ruby.RestartDelay = duration.Duration(5 * time.Minute)
	}

	if len(cfg.Ruby.Dir) == 0 {
		return fmt.Errorf("gitaly-ruby.dir: is not set")
	}

	minWorkers := 2
	if cfg.Ruby.NumWorkers < minWorkers {
		cfg.Ruby.NumWorkers = minWorkers
	}

	var err error
	cfg.Ruby.Dir, err = filepath.Abs(cfg.Ruby.Dir)
	if err != nil {
		return err
	}

	if len(cfg.Ruby.RuggedGitConfigSearchPath) != 0 {
		cfg.Ruby.RuggedGitConfigSearchPath, err = filepath.Abs(cfg.Ruby.RuggedGitConfigSearchPath)
		if err != nil {
			return err
		}
	}

	return validateIsDirectory(cfg.Ruby.Dir, "gitaly-ruby.dir")
}
