package config

import (
	"crypto/subtle"
	"fmt"
)

// Auth contains the authentication settings for this Gitaly process.
type Auth struct {
	Required bool  `toml:"required"`
	Token    Token `toml:"token"`
}

// Token is a string of the form "name:secret". It specifies a Gitaly
// authentication token.
type Token string

// Equal tests if t is equal to the token specified by name and secret.
func (t Token) Equal(other string) bool {
	return subtle.ConstantTimeCompare([]byte(other), []byte(t)) == 1
}

func validateToken() error {
	if !Config.Auth.Required {
		return nil
	}

	if len(Config.Auth.Token) == 0 {
		return fmt.Errorf("auth token may not be empty")
	}

	return nil
}
