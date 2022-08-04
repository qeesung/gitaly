//go:build gofuzz

package catfile

import (
	"bufio"
	"bytes"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

func Fuzz(data []byte) int {
	reader := bufio.NewReader(bytes.NewReader(data))
	ParseObjectInfo(git.ObjectHashSHA1, reader)
	return 0
}
