package balancer

func newPool() *pool {
	return &pool{active: make(map[string]struct{})}
}

// pool is a set that keeps one address (element) set aside as a standby.
// This data structure is not thread safe.
type pool struct {
	standby string
	active  map[string]struct{}
}

// add is idempotent. If there is no standby address yet, addr becomes
// the standby.
func (p *pool) add(addr string) {
	if _, ok := p.active[addr]; ok || p.standby == addr {
		return
	}

	if p.standby == "" {
		p.standby = addr
		return
	}

	p.active[addr] = struct{}{}
}

func (p *pool) activeSize() int {
	return len(p.active)
}

// remove tries to remove addr from the active addresses. If addr is not
// known or not active, remove returns false.
func (p *pool) remove(addr string) bool {
	if _, ok := p.active[addr]; !ok || p.standby == "" {
		return false
	}

	delete(p.active, addr)

	// Promote the standby to an active address
	p.active[p.standby] = struct{}{}
	p.standby = ""

	return true
}

// activeAddrs returns the currently active addresses as a list. The
// order is not deterministic.
func (p *pool) activeAddrs() []string {
	var addrs []string

	for a := range p.active {
		addrs = append(addrs, a)
	}

	return addrs
}
