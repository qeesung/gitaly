package supervisor

import (
	"fmt"
	"os/exec"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/kelseyhightower/envconfig"
)

// Config holds configuration for the circuit breaker of the respawn loop.
type Config struct {
	// GITALY_SUPERVISOR_CRASH_THRESHOLD
	CrashThreshold int `split_words:"true" default:"5"`
	// GITALY_SUPERVISOR_CRASH_WAIT_TIME
	CrashWaitTime time.Duration `split_words:"true" default:"1m"`
	// GITALY_SUPERVISOR_CRASH_RESET_TIME
	CrashResetTime time.Duration `split_words:"true" default:"1m"`
}

var (
	config Config
)

func init() {
	envconfig.MustProcess("gitaly_supervisor", &config)
}

// Process represents a running process.
type Process struct {
	Name string

	// Information to start the process
	env  []string
	args []string
	dir  string

	// Shutdown
	shutdown chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// New creates a new proces instance.
func New(name string, env []string, args []string, dir string) (*Process, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("need at least one argument")
	}

	p := &Process{
		Name:     name,
		env:      env,
		args:     args,
		dir:      dir,
		shutdown: make(chan struct{}),
		done:     make(chan struct{}),
	}

	go watch(p)
	return p, nil
}

func (p *Process) start(logger *log.Entry) (*exec.Cmd, error) {
	cmd := exec.Command(p.args[0], p.args[1:]...)
	cmd.Env = p.env
	cmd.Dir = p.dir
	cmd.Stdout = logger.WriterLevel(log.InfoLevel)
	cmd.Stderr = logger.WriterLevel(log.InfoLevel)
	return cmd, cmd.Start()
}

func watch(p *Process) {
	// Count crashes to prevent a tight respawn loop. This is a 'circuit breaker'.
	crashes := 0

	logger := log.WithField("supervisor.args", p.args).WithField("supervisor.name", p.Name)

	// Use a buffered channel because we don't want to block the respawn loop
	// on the monitor goroutine.
	monitorChan := make(chan monitorProcess, config.CrashThreshold)
	monitorDone := make(chan struct{})
	go monitorRss(monitorChan, monitorDone)

spawnLoop:
	for {
		if crashes >= config.CrashThreshold {
			logger.Warn("opening circuit breaker")
			select {
			case <-p.shutdown:
				break spawnLoop
			case <-time.After(config.CrashWaitTime):
				logger.Warn("closing circuit breaker")
				crashes = 0
			}
		}

		select {
		case <-p.shutdown:
			break spawnLoop
		default:
		}

		cmd, err := p.start(logger)
		if err != nil {
			crashes++
			logger.WithError(err).Error("start failed")
			continue
		}
		logger.WithField("supervisor.pid", cmd.Process.Pid).Warn("spawned")

		waitCh := make(chan struct{})
		go func() {
			logger.WithError(cmd.Wait()).Warn("exited")
			close(waitCh)
		}()

		monitorChan <- monitorProcess{name: p.Name, pid: cmd.Process.Pid, wait: waitCh}

	waitLoop:
		for {
			select {
			case <-time.After(config.CrashResetTime):
				crashes = 0
			case <-waitCh:
				crashes++
				break waitLoop
			case <-p.shutdown:
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				<-waitCh
				break spawnLoop
			}
		}
	}

	close(monitorChan)
	<-monitorDone
	close(p.done)
}

// Stop terminates the process.
func (p *Process) Stop() {
	if p == nil {
		return
	}

	p.stopOnce.Do(func() {
		close(p.shutdown)
	})

	select {
	case <-p.done:
	case <-time.After(1 * time.Second):
		// Don't wait for shutdown forever
	}
}
