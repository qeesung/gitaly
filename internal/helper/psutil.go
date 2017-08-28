package helper

import (
	"fmt"

	"github.com/shirou/gopsutil/process"
)

// PsUtil performs an int cast check before returning a gopsutil Process.
func PsUtil(pid int) (*process.Process, error) {
	if int64(int32(pid)) != int64(pid) {
		return nil, fmt.Errorf("PID %d cannot be cast to int32", pid)
	}

	return process.NewProcess(int32(pid))
}
