// +build darwin dragonfly freebsd

package fstype

import "golang.org/x/sys/unix"

// FileSystem will return the type of filesystem being used at the passed path
func FileSystem(path string) string {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return unknownFS
	}

	var buf []byte
	for _, c := range stat.Fstypename {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}

	if len(buf) == 0 {
		return unknownFS
	}

	return string(buf)
}
