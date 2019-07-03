package fstype

import "golang.org/x/sys/unix"

func detectFileSystem(path string) string {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return unknownFS
	}

	var buf []byte
	for _, c := range stat.F_fstypename {
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
