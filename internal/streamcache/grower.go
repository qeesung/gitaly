package streamcache

import (
	"sync"
)

// grower is a datatype that combines concurrent updates of an int64 with
// change notifications. The number is only allowed to go up; it is meant
// to represent the read or write offset in a file that is being accessed
// linearly.
type grower struct {
	v           int64
	subscribers []*notifier
	m           sync.Mutex
}

func (g *grower) Subscribe() *notifier {
	g.m.Lock()
	defer g.m.Unlock()

	n := newNotifier()
	g.subscribers = append(g.subscribers, n)
	return n
}

func (g *grower) Unsubscribe(n *notifier) {
	g.m.Lock()
	defer g.m.Unlock()

	for i := range g.subscribers {
		if g.subscribers[i] == n {
			g.subscribers = append(g.subscribers[:i], g.subscribers[i+1:]...)
			break
		}
	}
}

func (g *grower) HasSubscribers() bool {
	g.m.Lock()
	defer g.m.Unlock()
	return len(g.subscribers) != 0
}

// Grow grows g.v to the new value v. If v <= g.v this is a no-op. In the
// case that g.v actually grew, all subscribers are notified.
func (g *grower) Grow(v int64) {
	g.m.Lock()
	defer g.m.Unlock()

	if v > g.v {
		g.v = v
		for _, n := range g.subscribers {
			n.Notify()
		}
	}
}

func (g *grower) Value() int64 {
	g.m.Lock()
	defer g.m.Unlock()
	return g.v
}

type notifier struct{ C chan struct{} }

func newNotifier() *notifier { return &notifier{C: make(chan struct{}, 1)} }

func (n *notifier) Notify() {
	select {
	case n.C <- struct{}{}:
	default:
	}
}
