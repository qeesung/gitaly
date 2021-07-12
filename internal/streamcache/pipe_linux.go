package streamcache

import (
	"errors"
	"io"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	sendfileCounter = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_streamcache_sendfile_bytes_total",
			Help: "Number of bytes sent using sendfile",
		},
	)
)

func (pr *pipeReader) WriteTo(w io.Writer) (int64, error) {
	if n, err := pr.writeTo(w); n > 0 || err == nil {
		return n, err
	}

	// If n == 0 and err != nil then we were unable to use sendfile(2), so try
	// again using io.Copy and Read.
	return io.Copy(w, struct{ io.Reader }{pr})
}

func (pr *pipeReader) writeTo(w io.Writer) (int64, error) {
	dst, err := getRawconn(w)
	if err != nil {
		return 0, err
	}

	src, err := getRawconn(pr.reader)
	if err != nil {
		return 0, err
	}

	start := pr.position
	var errRead, errWrite, errSendfile error
	errRead = src.Read(func(srcFd uintptr) bool {
		errWrite = dst.Write(func(dstFd uintptr) bool {
			errSendfile = pr.sendfile(int(dstFd), int(srcFd))

			// If errSendfile is EAGAIN, ask Go runtime to wait for dst to become
			// writeable again.
			return errSendfile != syscall.EAGAIN
		})

		return true
	})
	written := pr.position - start

	for _, err := range []error{errRead, errWrite, errSendfile} {
		if err != nil {
			return written, err
		}
	}

	return written, nil
}

func getRawconn(v interface{}) (syscall.RawConn, error) {
	if sc, ok := v.(syscall.Conn); ok {
		return sc.SyscallConn()
	}

	return nil, errors.New("value does not implement syscall.Conn")
}

func (pr *pipeReader) sendfile(dst int, src int) error {
	for {
		const maxBytes = 4 << 20 // Same limit as Go stdlib
		sendBytes := maxBytes
		if available := pr.waitReadable(); available < int64(sendBytes) {
			sendBytes = int(available)
		}

		if sendBytes == 0 {
			return nil // end of file
		}

		// See https://man7.org/linux/man-pages/man2/sendfile.2.html
		n, err := syscall.Sendfile(dst, src, nil, sendBytes)

		// Guard against n being -1 on error
		if n > 0 {
			pr.advancePosition(n)
			sendfileCounter.Add(float64(n))
		}

		// In case of EINTR, retry immediately
		if err != nil && err != syscall.EINTR {
			return err
		}
	}
}
