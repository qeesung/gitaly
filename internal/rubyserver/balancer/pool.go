package balancer

func newPool() *pool {
	return &pool{active: make(map[string]struct{})}
}

type pool struct {
	standby string
	active  map[string]struct{}
}

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

func (p *pool) size() int {
	return len(p.active)
}

func (p *pool) remove(addr string) bool {
	if _, ok := p.active[addr]; !ok || p.standby == "" {
		return false
	}

	delete(p.active, addr)
	p.active[p.standby] = struct{}{}
	p.standby = ""

	return true
}

func (p *pool) addrs() []string {
	var result []string
	for a := range p.active {
		result = append(result, a)
	}

	return result
}
