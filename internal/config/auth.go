package config

import (
	"crypto/subtle"

	log "github.com/Sirupsen/logrus"
)

// Auth contains the authentication settings for this Gitaly process.
type Auth struct {
	Unenforced bool  `toml:"unenforced"`
	Token      Token `toml:"token"`
}

// Token is a string of the form "name:secret". It specifies a Gitaly
// authentication token.
type Token string

// Equal tests if t is equal to the token specified by name and secret.
func (t Token) Equal(other string) bool {
	return subtle.ConstantTimeCompare([]byte(other), []byte(t)) == 1
}

func validateToken() error {
	if !Config.Auth.Unenforced || len(Config.Auth.Token) == 0 {
		return nil
	}

	log.Warn("Authentication is enabled but not enforced. Gitaly will accept unauthenticated requests.")
	return nil
}
