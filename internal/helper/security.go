package helper

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var ErrRelativePathEscapesRoot = errors.New("relative path escapes root directory")

// ValidateRelativePath validates a relative path by joining it with rootDir and verifying the result
// is either rootDir or a path within rootDir. Returns clean relative path from rootDir to relativePath
// or an ErrRelativePathEscapesRoot if the resulting path is not contained within rootDir.
func ValidateRelativePath(rootDir, relativePath string) (string, error) {
	absPath := filepath.Join(rootDir, relativePath)
	if rootDir != absPath && !strings.HasPrefix(absPath, rootDir+string(os.PathSeparator)) {
		return "", ErrRelativePathEscapesRoot
	}

	return filepath.Rel(rootDir, absPath)
}

// Pattern taken from Regular Expressions Cookbook, slightly modified though
//                                        |Scheme                |User                         |Named/IPv4 host|IPv6+ host
var hostPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+\-.]*://)([a-z0-9\-._~%!$&'()*+,;=:]+@)([a-z0-9\-._~%]+|\[[a-z0-9\-._~%!$&'()*+,;=:]+\])`)

// SanitizeString will clean password and tokens from URLs, and replace them
// with [FILTERED].
func SanitizeString(str string) string {
	return hostPattern.ReplaceAllString(str, "$1[FILTERED]@$3$4")
}

// SanitizeError does the same thing as SanitizeString but for error types
func SanitizeError(err error) error {
	return errors.New(SanitizeString(err.Error()))
}
