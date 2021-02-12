package streamcache

import (
	"io"
	"os"
	"sync"
)

// Pipes
//
//            +-------+
//            |       | <- *pipeReader <- Read()
// Write() -> | *pipe |       |
//            |       |       |
//            +-------+       |
//                |           |
//                v           |
//           +----------+     |
//           |   file   | <---+
//           +----------+
//
// Pipes are called so because their interface and behavior somewhat
// resembles Unix pipes, except there are multiple readers as opposed to
// just one. Just like with Unix pipes, pipe readers exert backpressure
// on the writer. When the write end is closed, the readers see EOF, just
// like they would when reading a file. When the read end is closed
// before the writer is done, the writer receives an error. This is all
// like it is with Unix pipes.
//
// The differences are as follows. When you create a pipe you get a write
// end and a read end, just like with Unix pipes. But now you can open an
// additional reader by calling OpenReader on the write end (the *pipe
// instance). Under the covers, a Unix pipe is just a buffer, and you
// cannot "rewind" it. With our pipe, there is an underlying file, so a
// new reader starts at offset 0 in the file. It can then catch up with
// the other readers.
//
// The way backpressure works is that the writer looks at the fastest
// reader. More specifically, it looks at the maximum of the file offsets
// of all the readers. This is useful because imagine what happens when a
// pipe is created by a reader on a slow network connection. The slow
// reader slows down the writer. Now a fast reader joins. The fast reader
// should not have to wait for the slow reader. What will happen is that
// the fast reader will catch up with the slow reader and overtake it.
// From that point on, the writer moves faster too, because it feels the
// backpressure of the fastest reader.

type pipe struct {
	// Access to underlying file
	name      string
	w         io.WriteCloser
	closed    chan struct{}
	closeOnce sync.Once

	// Reader/writer coordination. If woffset > roffset, the writer blocks
	// (back pressure). If roffset >= woffset, the readers block (waiting for
	// new data).
	woffset *grower
	roffset *grower

	// wnotify is the channel the writer uses to wait for reader progress
	// notifications.
	wnotify *notifier
}

func newPipe(f *os.File) (io.ReadCloser, *pipe, error) {
	p := &pipe{
		name:    f.Name(),
		w:       f,
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
	err := os.ErrClosed
	p.closeOnce.Do(func() {
		close(p.closed)
		err = p.w.Close()
	})
	return err
}

func (p *pipe) Remove() error { return os.Remove(p.name) }

func (p *pipe) OpenReader() (io.ReadCloser, error) {
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
	p.woffset.Unsubscribe(pr.n)
	if !p.woffset.HasSubscribers() {
		p.Close()
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
wait:
	for pr.off >= pr.p.woffset.Value() {
		select {
		case <-pr.p.closed:
			break wait
		case <-pr.n.C:
		}
	}

	n, err := pr.r.Read(b)
	pr.off += int64(n)

	// The writer is subscribed to changes in pr.p.roffset. If it is
	// currently blocked, this call to Grow() will unblock it.
	pr.p.roffset.Grow(pr.off)

	return n, err
}
