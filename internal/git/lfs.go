package git

import (
	"bytes"
	"regexp"
	"strconv"
)

var (
	lfsOIDRe  = regexp.MustCompile(`(?m)^oid sha256:([0-9a-f]{64})$`)
	lfsSizeRe = regexp.MustCompile(`(?m)^size ([0-9]+)$`)
)

// IsLFSPointer checks to see if a blob is an LFS pointer.
// TODO: this is incomplete as it does not recognize pre-release version of LFS blobs with
// the "https://hawser.github.com/spec/v1" version. For compatibility with the Ruby RPC, we
// leave this as-is for now though.
func IsLFSPointer(b []byte) (bool, []byte, int64) {
	// ensure the version exists
	if !bytes.HasPrefix(b, []byte("version https://git-lfs.github.com/spec")) {
		return false, nil, -1
	}

	// ensure the oid exists and extract it
	oidExtract := lfsOIDRe.FindSubmatch(b)
	if len(oidExtract) != 2 {
		return false, nil, -1
	}

	// ensure the size exists
	sizeExtract := lfsSizeRe.FindSubmatch(b)
	if len(sizeExtract) != 2 {
		return false, nil, -1
	}

	size, err := strconv.ParseInt(string(sizeExtract[1]), 10, 64)
	if err != nil {
		return false, nil, -1
	}

	// Copy bytes to ensure b can be garbage collected
	oid := make([]byte, len(oidExtract[1]))
	copy(oid, oidExtract[1])
	return true, oid, size
}
