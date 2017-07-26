package diff

import (
	"os"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

var testRepo *pb.Repository

func TestMain(m *testing.M) {
	testRepo = testhelper.TestRepository()

	os.Exit(func() int {
		return m.Run()
	}())
}
