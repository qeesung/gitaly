package ps

import (
	"os/exec"
	"strconv"

	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
)

// Exec invokes ps -o keywords -p pid and returns its output
func Exec(pid int, keywords string) (string, error) {
	out, err := exec.Command("ps", "-o", keywords, "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}

	return text.ChompBytes(out), nil
}

// RSS invokes ps -o rss= -p pid and returns its output
func RSS(pid int) (int, error) {
	rss, err := Exec(pid, "rss=")
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(rss)
}

// Comm invokes ps -o comm= -p pid and returns its output
func Comm(pid int) (string, error) {
	return Exec(pid, "comm=")
}
