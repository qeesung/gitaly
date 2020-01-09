package inspect

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	content := []byte("test\x02data")
	pr, pw := io.Pipe()

	checked := make(chan struct{})

	ctx, cancel := context.WithCancel(context.TODO())

	writer := Write(ctx, pw, func(reader io.Reader) {
		data, err := ioutil.ReadAll(reader)
		require.NoError(t, err)
		require.Equal(t, content, data)
		close(checked)
	})

	go func() {
		_, err := io.Copy(writer, bytes.NewReader(content))
		require.NoError(t, err)
		require.NoError(t, pw.Close())
	}()

	data, err := ioutil.ReadAll(pr)
	require.NoError(t, err)
	require.Equal(t, content, data)

	cancel()
	<-checked
}

func TestLogPackInfoStatistic(t *testing.T) {
	dest := &bytes.Buffer{}
	log := &logrus.Logger{
		Out:       dest,
		Formatter: new(logrus.JSONFormatter),
		Level:     logrus.InfoLevel,
	}
	ctx := ctxlogrus.ToContext(context.Background(), log.WithField("test", "logging"))

	logging := LogPackInfoStatistic(ctx)
	logging(strings.NewReader("0038\x41ACK 1e292f8fedd741b75372e19097c76d327140c312 ready\n0035\x02Total 1044 (delta 519), reused 1035 (delta 512)\n0038\x41ACK 1e292f8fedd741b75372e19097c76d327140c312 ready\n0000\x01"))

	require.Contains(t, dest.String(), "Total 1044 (delta 519), reused 1035 (delta 512)")
	require.NotContains(t, dest.String(), "ACK 1e292f8fedd741b75372e19097c76d327140c312")
}
