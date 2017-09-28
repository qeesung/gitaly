package version

import (
	"fmt"
)

var version string
var buildtime string

// GetVersionString returns a standard version header
func GetVersionString() string {
	return fmt.Sprintf("Gitaly, version %v, built %v", version, buildtime)
}

// GetVersion returns the semver compatible version number
func GetVersion() string {
	return version
}

// GetBuildTime returns the time at which the build took place
func GetBuildTime() string {
	return buildtime
}
