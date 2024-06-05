package git

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// minimumVersion is the minimum required Git version. If updating this version, be sure to
// also update the following locations:
// - https://gitlab.com/gitlab-org/gitaly/blob/master/README.md#installation
// - https://gitlab.com/gitlab-org/gitaly/blob/master/.gitlab-ci.yml
// - https://docs.gitlab.com/ee/install/installation.html#software-requirements
// - https://docs.gitlab.com/ee/update/ (see e.g. https://docs.gitlab.com/ee/update/#1440)
var minimumVersion = Version{
	versionString: "2.42.0",
	major:         2,
	minor:         42,
	patch:         0,
	rc:            false,

	// gl is the GitLab patch level.
	gl: 0,
}

// Version represents the version of git itself.
type Version struct {
	// versionString is the string representation of the version. The string representation is
	// not directly derived from the parsed version information because we do not extract all
	// information from the original version string. Deriving it from parsed information would
	// thus potentially lose information.
	versionString       string
	major, minor, patch uint32
	rc                  bool
	gl                  uint32
}

// NewVersion constructs a new Git version from the given components.
func NewVersion(major, minor, patch, gl uint32) Version {
	return Version{
		versionString: fmt.Sprintf("%d.%d.%d.gl%d", major, minor, patch, gl),
		major:         major,
		minor:         minor,
		patch:         patch,
		gl:            gl,
	}
}

// parseVersionOutput parses output returned by git-version(1). It is expected to be in the format
// "git version 2.39.1.gl1".
func parseVersionOutput(versionOutput []byte) (Version, error) {
	trimmedVersionOutput := string(bytes.Trim(versionOutput, " \n"))
	versionString := strings.SplitN(trimmedVersionOutput, " ", 3)
	if len(versionString) != 3 {
		return Version{}, fmt.Errorf("invalid version format: %q", string(versionOutput))
	}

	version, err := parseVersion(versionString[2])
	if err != nil {
		return Version{}, fmt.Errorf("cannot parse git version: %w", err)
	}

	return version, nil
}

// SupportSymrefUpdates validates if the version supports 'symref-update' subcommands
// in 'git-update-ref'. This feature has not been tagged yet, so we use a high
// version number to build for it. This should be changed as the feature gets merged
// and released.
func (v Version) SupportSymrefUpdates() bool {
	return v.GreaterOrEqual(NewVersion(99, 99, 99, 0))
}

// String returns the string representation of the version.
func (v Version) String() string {
	return v.versionString
}

// IsSupported checks if a version string corresponds to a Git version
// supported by Gitaly.
func (v Version) IsSupported() bool {
	return !v.LessThan(minimumVersion)
}

// LessThan determines whether the version is older than another version.
func (v Version) LessThan(other Version) bool {
	switch {
	case v.major < other.major:
		return true
	case v.major > other.major:
		return false

	case v.minor < other.minor:
		return true
	case v.minor > other.minor:
		return false

	case v.patch < other.patch:
		return true
	case v.patch > other.patch:
		return false

	case v.rc && !other.rc:
		return true
	case !v.rc && other.rc:
		return false

	case v.gl < other.gl:
		return true
	case v.gl > other.gl:
		return false

	default:
		// this should only be reachable when versions are equal
		return false
	}
}

// GreaterOrEqual determines whether the version is newer than or equal to another version.
func (v Version) GreaterOrEqual(other Version) bool {
	return !v.LessThan(other)
}

func parseVersion(versionStr string) (Version, error) {
	versionSplit := strings.SplitN(versionStr, ".", 4)
	if len(versionSplit) < 3 {
		return Version{}, fmt.Errorf("expected major.minor.patch in %q", versionStr)
	}

	ver := Version{
		versionString: versionStr,
	}

	for i, v := range []*uint32{&ver.major, &ver.minor, &ver.patch} {
		var n64 uint64

		if versionSplit[i] == "GIT" {
			// Git falls back to vx.x.GIT if it's unable to describe the current version
			// or if there's a version file. We should just treat this as "0", even
			// though it may have additional commits on top.
			n64 = 0
		} else {
			rcSplit := strings.SplitN(versionSplit[i], "-", 2)

			var err error
			n64, err = strconv.ParseUint(rcSplit[0], 10, 32)
			if err != nil {
				return Version{}, err
			}

			if len(rcSplit) == 2 && strings.HasPrefix(rcSplit[1], "rc") {
				ver.rc = true
			}
		}

		*v = uint32(n64)
	}

	if len(versionSplit) == 4 {
		if strings.HasPrefix(versionSplit[3], "rc") {
			ver.rc = true
		} else if strings.HasPrefix(versionSplit[3], "gl") {
			gitlabPatchLevel := versionSplit[3][2:]

			gl, err := strconv.ParseUint(gitlabPatchLevel, 10, 32)
			if err != nil {
				return Version{}, err
			}

			ver.gl = uint32(gl)
		}
	}

	return ver, nil
}
