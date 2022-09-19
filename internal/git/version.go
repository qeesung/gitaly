package git

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
)

// minimumVersion is the minimum required Git version. If updating this version, be sure to
// also update the following locations:
// - https://gitlab.com/gitlab-org/gitaly/blob/master/README.md#installation
// - https://gitlab.com/gitlab-org/gitaly/blob/master/.gitlab-ci.yml
// - https://gitlab.com/gitlab-org/gitlab-foss/blob/master/.gitlab-ci.yml
// - https://gitlab.com/gitlab-org/gitlab-foss/blob/master/lib/system_check/app/git_version_check.rb
var minimumVersion = Version{
	versionString: "2.36.0",
	major:         2,
	minor:         36,
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

func parseVersionFromCommand(cmd *command.Command) (Version, error) {
	versionOutput, err := io.ReadAll(cmd)
	if err != nil {
		return Version{}, fmt.Errorf("reading version output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return Version{}, fmt.Errorf("waiting for version command: %w", err)
	}

	trimmedVersionOutput := strings.Trim(string(versionOutput), " \n")
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

// String returns the string representation of the version.
func (v Version) String() string {
	return v.versionString
}

// IsSupported checks if a version string corresponds to a Git version
// supported by Gitaly.
func (v Version) IsSupported() bool {
	return !v.LessThan(minimumVersion)
}

// FlushesUpdaterefStatus determines whether the given Git version properly flushes status messages
// in git-update-ref(1).
func (v Version) FlushesUpdaterefStatus() bool {
	// This has been released with v2.33.1 and will be part of v2.34.0. It's also contained in
	// our custom-patched version v2.33.0-gl3. So everything newer than these versions is
	// supported.
	return !v.LessThan(Version{
		major: 2, minor: 33, patch: 0, gl: 3,
	})
}

// HasGranularFsyncConfig determines whether the given Git version supports the new granular fsync
// configuration via `core.fsync`. This new config has been introduced with Git v2.36.0.
func (v Version) HasGranularFsyncConfig() bool {
	return !v.LessThan(Version{major: 2, minor: 36, rc: true})
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
