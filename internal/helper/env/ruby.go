package env

import "strings"

// AllowedRubyEnvironment filters the given set of environment variables and returns only those
// which are allowed when executing Ruby commands.
func AllowedRubyEnvironment(environmentVariables []string) []string {
	var filteredEnv []string
	for _, environmentVariable := range environmentVariables {
		for _, prefix := range []string{
			"BUNDLE_PATH=",
			"BUNDLE_APP_CONFIG=",
			"BUNDLE_USER_CONFIG=",
			"GEM_HOME=",
		} {
			if strings.HasPrefix(environmentVariable, prefix) {
				filteredEnv = append(filteredEnv, environmentVariable)
			}
		}
	}
	return filteredEnv
}
