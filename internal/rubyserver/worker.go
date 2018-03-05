package rubyserver

import (
	"fmt"
	"syscall"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver/balancer"
	"gitlab.com/gitlab-org/gitaly/internal/supervisor"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var (
	terminationCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_ruby_memory_terminations_total",
			Help: "Number of times gitaly-ruby has been terminated because of excessive memory use.",
		},
		[]string{"name"},
	)
)

func init() {
	prometheus.MustRegister(terminationCounter)
}

// worker observes the event stream of a supervised process and restarts
// it if necessary, in cooperation with the balancer.
type worker struct {
	*supervisor.Process
	address string
	events  <-chan supervisor.Event

	// This is for testing only, so that we can inject a fake balancer
	balancerUpdate chan balancerProxy
}

func newWorker(p *supervisor.Process, address string, events <-chan supervisor.Event) *worker {
	w := &worker{
		Process:        p,
		address:        address,
		events:         events,
		balancerUpdate: make(chan balancerProxy),
	}
	go w.monitor()

	bal := defaultBalancer{}
	w.balancerUpdate <- bal

	// When we return from this function, requests may start coming in. If
	// there are no addresses in the balancer when the first request comes in
	// we can get a panic from grpc-go. So before returning, we ensure the
	// current address has been added to the balancer.
	bal.AddAddress(w.address)

	return w
}

type balancerProxy interface {
	AddAddress(string)
	RemoveAddress(string) bool
}

type defaultBalancer struct{}

func (defaultBalancer) AddAddress(s string)         { balancer.AddAddress(s) }
func (defaultBalancer) RemoveAddress(s string) bool { return balancer.RemoveAddress(s) }

func (w *worker) monitor() {
	sw := &stopwatch{}
	currentPid := 0
	bal := <-w.balancerUpdate

	for {
	nextEvent:
		select {
		case e := <-w.events:
			if e.Pid <= 0 {
				log.WithFields(log.Fields{
					"worker.name":      w.Name,
					"worker.event_pid": e.Pid,
				}).Info("received invalid PID")
				break nextEvent
			}

			switch e.Type {
			case supervisor.Up:
				if e.Pid == currentPid {
					// Ignore repeated events to avoid constantly resetting our internal
					// state.
					break nextEvent
				}

				bal.AddAddress(w.address)
				currentPid = e.Pid
				sw.reset()
			case supervisor.MemoryHigh:
				if e.Pid != currentPid {
					break nextEvent
				}

				sw.mark()
				if sw.elapsed() <= config.Config.Ruby.RestartDelay {
					break nextEvent
				}

				// It is crucial to check the return value of RemoveAddress. If we don't
				// we may leave the system without the capacity to make gitaly-ruby
				// requests.
				if bal.RemoveAddress(w.address) {
					go w.waitTerminate(e.Pid)
					sw.reset()
				}
			case supervisor.MemoryLow:
				if e.Pid != currentPid {
					break nextEvent
				}

				sw.reset()
			default:
				panic(fmt.Sprintf("unknown state %v", e.Type))
			}
		case bal = <-w.balancerUpdate:
			// For testing only.
		}
	}
}

func (w *worker) waitTerminate(pid int) {
	// Wait for in-flight requests to reach the worker before we slam the
	// door in their face.
	time.Sleep(1 * time.Minute)

	terminationCounter.WithLabelValues(w.Name).Inc()

	log.WithFields(log.Fields{
		"worker.name": w.Name,
		"worker.pid":  pid,
	}).Info("sending SIGTERM")
	syscall.Kill(pid, syscall.SIGTERM)

	time.Sleep(config.Config.Ruby.GracefulRestartTimeout)

	log.WithFields(log.Fields{
		"worker.name": w.Name,
		"worker.pid":  pid,
	}).Info("sending SIGKILL")
	syscall.Kill(pid, syscall.SIGKILL)
}
