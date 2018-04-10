package config

import (
	log "github.com/sirupsen/logrus"
)

// Auth contains the authentication settings for this Gitaly process.
type Auth struct {
	Transitioning bool   `toml:"transitioning"`
	Token         string `toml:"token"`
}

func validateToken() error {
	if !Config.Auth.Transitioning || len(Config.Auth.Token) == 0 {
		return nil
	}

	log.Warn("Authentication is enabled but not enforced because transitioning=true. Gitaly will accept unauthenticated requests.")
	return nil
}
