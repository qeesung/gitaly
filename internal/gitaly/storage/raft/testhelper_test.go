package raft

import (
	"fmt"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestMain(m *testing.M) {
	// It's unfortunate that dragonboat's logger is global. It allows to configure the logger once.
	// Thus, we create one logger here to capture all system logs. The logs are dumped out once if
	// any of the test fails. It's not ideal, but better than no logs.
	logger, buffer := testhelper.NewCapturedLogger()
	SetLogger(logger, false)
	testhelper.Run(m, testhelper.WithTeardown(func(code int) error {
		if code != 0 {
			fmt.Printf("Recorded Raft's system logs from all the tests:\n%s\n", buffer)
		}
		return nil
	}))
}
