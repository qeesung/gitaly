package config

import (
	"fmt"
	"path/filepath"
	"time"
)

// Ruby contains setting for Ruby worker processes
type Ruby struct {
	Dir                       string   `toml:"dir"`
	MaxRSS                    int      `toml:"max_rss"`
	GracefulRestartTimeout    Duration `toml:"graceful_restart_timeout"`
	RestartDelay              Duration `toml:"restart_delay"`
	NumWorkers                int      `toml:"num_workers"`
	LinguistLanguagesPath     string   `toml:"linguist_languages_path"`
	RuggedGitConfigSearchPath string   `toml:"rugged_git_config_search_path"`
}

// Duration is a trick to let our TOML library parse durations from strings.
type Duration time.Duration

func (d *Duration) Duration() time.Duration {
	if d != nil {
		return time.Duration(*d)
	}
	return 0
}

func (d *Duration) UnmarshalText(text []byte) error {
	td, err := time.ParseDuration(string(text))
	if err == nil {
		*d = Duration(td)
	}
	return err
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// ConfigureRuby validates the gitaly-ruby configuration and sets default values.
func ConfigureRuby() error {
	if Config.Ruby.GracefulRestartTimeout.Duration() == 0 {
		Config.Ruby.GracefulRestartTimeout = Duration(10 * time.Minute)
	}

	if Config.Ruby.MaxRSS == 0 {
		Config.Ruby.MaxRSS = 200 * 1024 * 1024
	}

	if Config.Ruby.RestartDelay.Duration() == 0 {
		Config.Ruby.RestartDelay = Duration(5 * time.Minute)
	}

	if len(Config.Ruby.Dir) == 0 {
		return fmt.Errorf("gitaly-ruby.dir is not set")
	}

	minWorkers := 2
	if Config.Ruby.NumWorkers < minWorkers {
		Config.Ruby.NumWorkers = minWorkers
	}

	var err error
	Config.Ruby.Dir, err = filepath.Abs(Config.Ruby.Dir)
	if err != nil {
		return err
	}

	if len(Config.Ruby.RuggedGitConfigSearchPath) != 0 {
		Config.Ruby.RuggedGitConfigSearchPath, err = filepath.Abs(Config.Ruby.RuggedGitConfigSearchPath)
		if err != nil {
			return err
		}
	}

	return validateIsDirectory(Config.Ruby.Dir, "gitaly-ruby.dir")
}
