package streamcache

import (
	"bytes"
	"io"
	"io/ioutil"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func createPipe(t *testing.T) (io.ReadCloser, *pipe, func()) {
	t.Helper()

	f, err := ioutil.TempFile("", "gitaly-streamcache-test")
	require.NoError(t, err)

	pr, p, err := newPipe(f)
	require.NoError(t, err)

	return pr, p, func() {
		_ = p.Remove()
		p.Close()
	}
}

func writeBytes(w io.WriteCloser, buf []byte, progress *int64) error {
	for i := 0; i < len(buf); i++ {
		n, err := w.Write(buf[i : i+1])
		if err != nil {
			return err
		}
		if n != 1 {
			return io.ErrShortWrite
		}
		if progress != nil {
			atomic.AddInt64(progress, int64(n))
		}
	}
	return w.Close()
}

func TestPipe(t *testing.T) {
	pr, p, clean := createPipe(t)
	defer clean()

	readers := []io.ReadCloser{pr}
	defer func() {
		for _, r := range readers {
			r.Close()
		}
	}()

	input := make([]byte, 4096)
	n, err := rand.Read(input)
	require.NoError(t, err)
	require.Equal(t, len(input), n)

	wg := &sync.WaitGroup{}

	const N = 10
	for len(readers) < N {
		r, err := p.OpenReader()
		require.NoError(t, err)
		readers = append(readers, r)
	}

	output := make([]bytes.Buffer, N)
	outErrors := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, outErrors[i] = io.Copy(&output[i], readers[i])
		}(i)
	}

	werr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		werr <- writeBytes(p, input, nil)
	}()

	require.NoError(t, <-werr)
	wg.Wait()

	for i := 0; i < N; i++ {
		require.Equal(t, input, output[i].Bytes())
		require.NoError(t, outErrors[i])
	}
}

func TestPipe_backpressure(t *testing.T) {
	pr, p, clean := createPipe(t)
	defer clean()
	defer p.Close()
	defer pr.Close()

	input := "hello world"
	werr := make(chan error, 1)
	var wprogress int64
	go func() { werr <- writeBytes(p, []byte(input), &wprogress) }()

	var output []byte

	buf := make([]byte, 1)

	_, err := io.ReadFull(pr, buf)
	require.NoError(t, err)
	output = append(output, buf...)
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, int64(2), atomic.LoadInt64(&wprogress), "writer should be blocked after 2 writes")

	_, err = io.ReadFull(pr, buf)
	require.NoError(t, err)
	output = append(output, buf...)
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, int64(3), atomic.LoadInt64(&wprogress), "writer should be blocked after having advanced 1 write")

	rest, err := ioutil.ReadAll(pr)
	require.NoError(t, err)
	output = append(output, rest...)
	require.Equal(t, input, string(output))

	require.NoError(t, <-werr)
}

func TestPipe_closeWhenAllReadersLeave(t *testing.T) {
	pr1, p, clean := createPipe(t)
	defer clean()
	defer p.Close()
	defer pr1.Close()

	werr := make(chan error, 1)
	go func() { werr <- writeBytes(p, []byte("hello world"), nil) }()

	pr2, err := p.OpenReader()
	require.NoError(t, err)
	defer pr2.Close()

	// Sanity check
	select {
	case <-werr:
		t.Fatal("writer should still be blocked")
	default:
	}

	require.NoError(t, pr1.Close())
	time.Sleep(1 * time.Millisecond)

	select {
	case <-werr:
		t.Fatal("writer should still be blocked because there is still an active reader")
	default:
	}

	buf := make([]byte, 1)
	_, err = io.ReadFull(pr2, buf)
	require.NoError(t, err)
	require.Equal(t, "h", string(buf))

	require.NoError(t, pr2.Close())
	time.Sleep(1 * time.Millisecond)

	require.Error(t, <-werr, "writer should see error if all readers close before writer is done")
}
