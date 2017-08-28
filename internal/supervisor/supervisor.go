package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus"
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

	rssGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gitaly_supervisor_rss_bytes",
			Help: "Resident set size of supervised processes, in bytes.",
		},
		[]string{"name"},
	)
)

func init() {
	envconfig.MustProcess("gitaly_supervisor", &config)
	prometheus.MustRegister(rssGauge)
}

// Process represents a running process.
type Process struct {
	Name string

	// Information to start the process
	env  []string
	args []string
	dir  string

	// Shutdown
	done     chan struct{}
	stopOnce sync.Once
}

// New creates a new proces instance.
func New(name string, env []string, args []string, dir string) (*Process, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("need at least one argument")
	}

	p := &Process{
		Name: name,
		env:  env,
		args: args,
		dir:  dir,
		done: make(chan struct{}),
	}

	go watch(p)
	return p, nil
}

func (p *Process) start() (*exec.Cmd, error) {
	cmd := exec.Command(p.args[0], p.args[1:]...)
	cmd.Env = p.env
	cmd.Dir = p.dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, cmd.Start()
}

func watch(p *Process) {
	// Count crashes to prevent a tight respawn loop. This is a 'circuit breaker'.
	crashes := 0

	logger := log.WithField("supervisor.args", p.args).WithField("supervisor.name", p.Name)

	for {
		if crashes >= config.CrashThreshold {
			logger.Warn("opening circuit breaker")
			select {
			case <-p.done:
				return
			case <-time.After(config.CrashWaitTime):
				logger.Warn("closing circuit breaker")
				crashes = 0
			}
		}

		cmd, err := p.start()
		if err != nil {
			crashes++
			logger.WithError(err).Error("start failed")
			continue
		}

		waitCh := make(chan struct{})
		go func() {
			logger.WithError(cmd.Wait()).Warn("exited")
			close(waitCh)
		}()

		go monitorRss(p.Name, cmd.Process.Pid, waitCh)

	waitLoop:
		for {
			select {
			case <-time.After(config.CrashResetTime):
				crashes = 0
			case <-waitCh:
				crashes++
				break waitLoop
			case <-p.done:
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				return
			}
		}
	}
}

func monitorRss(name string, pid int, done <-chan struct{}) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()

	for {
		rssGauge.WithLabelValues(name).Set(float64(1024 * getRss(pid)))

		select {
		case <-done:
			return
		case <-t.C:
		}
	}
}

// getRss returns RSS in kilobytes.
func getRss(pid int) int {
	// I tried adding a library to do this but it seemed like overkill
	// and YAGNI compared to doing this one 'ps' call.
	psRss, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}

	rss, err := strconv.Atoi(strings.TrimSpace(string(psRss)))
	if err != nil {
		return 0
	}

	return rss
}

// Stop terminates the process.
func (p *Process) Stop() {
	if p == nil {
		return
	}

	p.stopOnce.Do(func() {
		close(p.done)
	})
}
