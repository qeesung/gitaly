//go:build !gitaly_test_sha256

package stats

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}
