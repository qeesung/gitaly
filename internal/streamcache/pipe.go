package streamcache

import (
	"errors"
	"io"
	"os"
	"sync"
)

func newPipe(name string, w io.WriteCloser) (io.ReadCloser, *pipe, error) {
	p := &pipe{
		name:    name,
		w:       w,
		woffset: &grower{},
		roffset: &grower{},
		closed:  make(chan struct{}),
	}
	p.wnotify = p.roffset.Subscribe()

	pr, err := p.OpenReader()
	if err != nil {
		return nil, nil, err
	}

	return pr, p, nil
}

type pipe struct {
	// Access to underlying file
	name   string
	w      io.WriteCloser
	closed chan struct{}
	m      sync.Mutex
	broken bool

	// Reader/writer coordination. If woffset > roffset, the writer blocks
	// (back pressure). If roffset >= woffset, the readers block (waiting for
	// new data).
	woffset *grower
	roffset *grower

	// wnotify is the channel the writer uses to wait for reader progress
	// notifications.
	wnotify *notifier
}

func (p *pipe) Write(b []byte) (int, error) {
	// Loop (block) until at least one reader catches up with our last write.
	for p.woffset.Value() > p.roffset.Value() {
		select {
		case <-p.closed:
			return 0, io.ErrClosedPipe
		case <-p.wnotify.C:
		}
	}

	n, err := p.w.Write(b)

	// Notify blocked readers, if any, of new data that is available.
	p.woffset.Grow(p.woffset.Value() + int64(n))

	return n, err
}

func (p *pipe) Close() error {
	p.m.Lock()
	defer p.m.Unlock()
	return p.close(false)
}

func (p *pipe) close(broken bool) error {
	select {
	case <-p.closed:
		return os.ErrClosed
	default:
		p.broken = broken
		close(p.closed)
		return p.w.Close()
	}
}

func (p *pipe) isBroken() bool {
	select {
	case <-p.closed:
		return p.broken
	default:
		return false
	}
}

var errBrokenPipe = errors.New("broken pipe: readers closed before writer")

func (p *pipe) OpenReader() (io.ReadCloser, error) {
	p.m.Lock()
	defer p.m.Unlock()

	if p.isBroken() {
		return nil, errBrokenPipe
	}

	r, err := os.Open(p.name)
	if err != nil {
		return nil, err
	}

	pr := &pipeReader{
		p: p,
		r: r,
		n: p.woffset.Subscribe(),
	}

	return pr, nil
}

func (p *pipe) closeReader(pr *pipeReader) {
	p.m.Lock()
	defer p.m.Unlock()

	p.woffset.Unsubscribe(pr.n)
	if !p.woffset.HasSubscribers() {
		p.close(true)
	}
}

type pipeReader struct {
	p   *pipe
	r   io.ReadCloser
	off int64
	n   *notifier
}

func (pr *pipeReader) Close() error {
	pr.p.closeReader(pr)
	return pr.r.Close()
}

func (pr *pipeReader) Read(b []byte) (int, error) {
	// If pr.off < pr.p.woffset.Value(), then there is unread data for us to
	// yield. If not, block.
	pipeclosed := false
	for !pipeclosed && pr.off >= pr.p.woffset.Value() {
		select {
		case <-pr.p.closed:
			pipeclosed = true
		case <-pr.n.C:
		}
	}

	if pr.p.isBroken() {
		return 0, errBrokenPipe
	}

	n, err := pr.r.Read(b)
	pr.off += int64(n)

	// The writer is subscribed to changes in pr.p.roffset. If it is
	// currently blocked, this call to Grow() will unblock it.
	pr.p.roffset.Grow(pr.off)

	if err == io.EOF && !pipeclosed {
		err = nil
	}

	return n, err
}
