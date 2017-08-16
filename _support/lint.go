package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if err := lint(); err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
}

func lint() (err error) {
	cmd := exec.Command("golint", "./...")
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		if waitErr := cmd.Wait(); waitErr != nil {
			err = fmt.Errorf("wait error: %v", waitErr)
		}
	}()

	scanner := bufio.NewScanner(stdout)
	offenses := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "vendor/") || strings.HasPrefix(line, "ruby/vendor/") {
			// We cannot and should not "fix" vendored code.
			continue
		}
		offenses++
		fmt.Println(line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %v", err)
	}

	if offenses > 0 {
		return fmt.Errorf("golint found %d offense(s)", offenses)
	}
	return nil
}
