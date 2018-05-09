package config

import (
	"fmt"
	"time"
)

// Ruby contains setting for Ruby worker processes
type Ruby struct {
	Dir                        string `toml:"dir"`
	MaxRSS                     int    `toml:"max_rss"`
	GracefulRestartTimeout     time.Duration
	GracefulRestartTimeoutToml duration `toml:"graceful_restart_timeout"`
	RestartDelay               time.Duration
	RestartDelayToml           duration `toml:"restart_delay"`
	NumWorkers                 int      `toml:"num_workers"`
	LinguistLanguagesPath      string   `toml:"linguist_languages_path"`
}

// This type is a trick to let our TOML library parse durations from strings.
type duration struct {
	time.Duration
}

func (d *duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

// ConfigureRuby validates the gitaly-ruby configuration and sets default values.
func ConfigureRuby() error {
	Config.Ruby.GracefulRestartTimeout = Config.Ruby.GracefulRestartTimeoutToml.Duration
	if Config.Ruby.GracefulRestartTimeout == 0 {
		Config.Ruby.GracefulRestartTimeout = 10 * time.Minute
	}

	if Config.Ruby.MaxRSS == 0 {
		Config.Ruby.MaxRSS = 300 * 1024 * 1024
	}

	Config.Ruby.RestartDelay = Config.Ruby.RestartDelayToml.Duration
	if Config.Ruby.RestartDelay == 0 {
		Config.Ruby.RestartDelay = 5 * time.Minute
	}

	if len(Config.Ruby.Dir) == 0 {
		return fmt.Errorf("gitaly-ruby.dir is not set")
	}

	minWorkers := 2
	if Config.Ruby.NumWorkers < minWorkers {
		Config.Ruby.NumWorkers = minWorkers
	}

	return validateIsDirectory(Config.Ruby.Dir, "gitaly-ruby.dir")
}
