package inspect

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
)

// NewWriter returns Writer that will feed 'action' with data on each write to it.
// The 'reader' for 'action' would be closed when ctx would be cancelled/expired.
func NewWriter(writer io.Writer, action func(reader io.Reader)) io.WriteCloser {
	pr, pw := io.Pipe()

	multiOut := io.MultiWriter(writer, pw)

	go func() {
		action(pr)
		// we need to be sure that all data consumed from pipe otherwise write to multi writer would be blocked
		_, _ = io.Copy(ioutil.Discard, pr)
	}()

	return struct {
		io.Writer
		io.Closer
	}{
		Writer: multiOut,
		Closer: pw,
	}
}

// LogPackInfoStatistic inspect data stream for the informational messages
// and logs info about pack file usage.
func LogPackInfoStatistic(ctx context.Context) func(reader io.Reader) {
	return func(reader io.Reader) {
		logger := ctxlogrus.Extract(ctx)

		scanner := pktline.NewScanner(reader)
		for scanner.Scan() {
			pktData := pktline.Data(scanner.Bytes())
			if !bytes.HasPrefix(pktData, []byte("\x02Total ")) {
				continue
			}

			logger.WithField("pack.stat", text.ChompBytes(pktData[1:])).Info("pack file compression statistic")
		}
		// we are not interested in scanner.Err()
	}
}
