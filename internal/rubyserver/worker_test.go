package rubyserver

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/supervisor"
)

func TestWorker(t *testing.T) {
	restartDelay := 10 * time.Millisecond

	defer func(old time.Duration) {
		config.Config.Ruby.RestartDelay = config.Duration(old)
	}(config.Config.Ruby.RestartDelay.Duration())
	config.Config.Ruby.RestartDelay = config.Duration(restartDelay)

	events := make(chan supervisor.Event)
	addr := "the address"
	w := newWorker(&supervisor.Process{Name: "testing"}, addr, events, true)
	defer w.stopMonitor()

	t.Log("ignore health failures during startup")
	mustIgnore(t, w, func() { events <- healthBadEvent() })

	firstPid := 123

	t.Log("register first PID as 'up'")
	mustAdd(t, w, addr, func() { events <- upEvent(firstPid) })

	t.Log("ignore repeated up event")
	mustIgnore(t, w, func() { events <- upEvent(firstPid) })

	t.Log("send mem high events but too fast to trigger restart")
	for i := 0; i < 5; i++ {
		mustIgnore(t, w, func() { events <- memHighEvent(firstPid) })
	}

	t.Log("mem low resets mem high counter")
	mustIgnore(t, w, func() { events <- memLowEvent(firstPid) })

	t.Log("send mem high events but too fast to trigger restart")
	for i := 0; i < 5; i++ {
		mustIgnore(t, w, func() { events <- memHighEvent(firstPid) })
	}

	time.Sleep(2 * restartDelay)
	t.Log("this mem high should push us over the threshold")
	mustRemove(t, w, addr, func() { events <- memHighEvent(firstPid) })

	t.Log("ignore health failures during startup")
	mustIgnore(t, w, func() { events <- healthBadEvent() })

	secondPid := 456
	t.Log("registering a new PID")
	mustAdd(t, w, addr, func() { events <- upEvent(secondPid) })

	t.Log("ignore mem high events for the previous pid")
	mustIgnore(t, w, func() { events <- memHighEvent(firstPid) })
	time.Sleep(2 * restartDelay)
	t.Log("ignore mem high also after restart delay has expired")
	mustIgnore(t, w, func() { events <- memHighEvent(firstPid) })

	t.Log("start high memory timer")
	mustIgnore(t, w, func() { events <- memHighEvent(secondPid) })

	t.Log("ignore mem low event for wrong pid")
	mustIgnore(t, w, func() { events <- memLowEvent(firstPid) })

	t.Log("send mem high count over the threshold")
	time.Sleep(2 * restartDelay)
	mustRemove(t, w, addr, func() { events <- memHighEvent(secondPid) })
}

func TestWorkerHealthChecks(t *testing.T) {
	restartDelay := 10 * time.Millisecond

	defer func(old time.Duration) {
		healthRestartDelay = old
	}(healthRestartDelay)
	healthRestartDelay = restartDelay

	defer func(old time.Duration) {
		healthRestartCoolOff = old
	}(healthRestartCoolOff)
	healthRestartCoolOff = restartDelay

	events := make(chan supervisor.Event)
	addr := "the address"
	w := newWorker(&supervisor.Process{Name: "testing"}, addr, events, true)
	defer w.stopMonitor()

	t.Log("ignore health failures during startup")
	mustIgnore(t, w, func() { events <- healthBadEvent() })

	firstPid := 123

	t.Log("register first PID as 'up'")
	mustAdd(t, w, addr, func() { events <- upEvent(firstPid) })

	t.Log("still ignore health failures during startup")
	mustIgnore(t, w, func() { events <- healthBadEvent() })

	time.Sleep(2 * restartDelay)

	t.Log("waited long enough, this health check should start health timer")
	mustIgnore(t, w, func() { events <- healthBadEvent() })

	time.Sleep(2 * restartDelay)

	t.Log("this second failed health check should trigger failover")
	mustRemove(t, w, addr, func() { events <- healthBadEvent() })

	t.Log("ignore extra health failures")
	mustIgnore(t, w, func() { events <- healthBadEvent() })
}

func mustIgnore(t *testing.T, w *worker, f func()) {
	nothing := &nothingBalancer{t}
	w.balancerUpdate <- nothing
	t.Log("executing function that should be ignored by balancer")
	f()
	// This second balancer update is used to synchronize with the monitor
	// goroutine. When the channel send finishes, we know the event we sent
	// before must have been processed.
	w.balancerUpdate <- nothing
}

func mustAdd(t *testing.T, w *worker, addr string, f func()) {
	add := newAdd(t, addr)
	w.balancerUpdate <- add
	t.Log("executing function that should lead to balancer.AddAddress")
	f()
	add.wait()
}

func mustRemove(t *testing.T, w *worker, addr string, f func()) {
	remove := newRemove(t, addr)
	w.balancerUpdate <- remove
	t.Log("executing function that should lead to balancer.RemoveAddress")
	f()
	remove.wait()
}

func waitFail(t *testing.T, done chan struct{}) {
	select {
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for balancer method call")
	case <-done:
	}
}

func upEvent(pid int) supervisor.Event {
	return supervisor.Event{Type: supervisor.Up, Pid: pid}
}

func memHighEvent(pid int) supervisor.Event {
	return supervisor.Event{Type: supervisor.MemoryHigh, Pid: pid}
}

func memLowEvent(pid int) supervisor.Event {
	return supervisor.Event{Type: supervisor.MemoryLow, Pid: pid}
}

func healthBadEvent() supervisor.Event {
	return supervisor.Event{Type: supervisor.HealthBad, Error: errors.New("test bad health")}
}

func newAdd(t *testing.T, addr string) *addBalancer {
	return &addBalancer{
		t:    t,
		addr: addr,
		done: make(chan struct{}),
	}
}

type addBalancer struct {
	addr string
	t    *testing.T
	done chan struct{}
}

func (ab *addBalancer) RemoveAddress(string) bool {
	ab.t.Fatal("unexpected RemoveAddress call")
	return false
}

func (ab *addBalancer) AddAddress(s string) {
	require.Equal(ab.t, ab.addr, s, "addBalancer expected AddAddress argument")
	close(ab.done)
}

func (ab *addBalancer) wait() {
	waitFail(ab.t, ab.done)
}

func newRemove(t *testing.T, addr string) *removeBalancer {
	return &removeBalancer{
		t:    t,
		addr: addr,
		done: make(chan struct{}),
	}
}

type removeBalancer struct {
	addr string
	t    *testing.T
	done chan struct{}
}

func (rb *removeBalancer) RemoveAddress(s string) bool {
	require.Equal(rb.t, rb.addr, s, "removeBalancer expected RemoveAddress argument")
	close(rb.done)
	return true
}

func (rb *removeBalancer) AddAddress(s string) {
	rb.t.Fatal("unexpected AddAddress call")
}

func (rb *removeBalancer) wait() {
	waitFail(rb.t, rb.done)
}

type nothingBalancer struct {
	t *testing.T
}

func (nb *nothingBalancer) RemoveAddress(s string) bool {
	nb.t.Fatal("unexpected RemoveAddress call")
	return true
}

func (nb *nothingBalancer) AddAddress(s string) {
	nb.t.Fatal("unexpected AddAddress call")
}
