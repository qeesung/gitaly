package supervisor

import (
	"fmt"
	"os"
	"os/exec"
)

// Process represents a running process.
type Process struct {
	cmd *exec.Cmd
}

// New creates a new proces instance.
func New(env []string, args []string, dir string) (*Process, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("need at least one argument")
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// TODO: spawn goroutine that watches (restarts) this process.
	return &Process{cmd: cmd}, cmd.Start()
}

// Stop terminates the process.
func (p *Process) Stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}

	process := p.cmd.Process
	process.Kill()
	process.Wait()
}
