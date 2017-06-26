package streamio

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestReceiveSources(t *testing.T) {
	testData := "Hello this is the test data that will be received"
	testCases := []struct {
		desc string
		r    io.Reader
	}{
		{desc: "base", r: strings.NewReader(testData)},
		{desc: "dataerr", r: iotest.DataErrReader(strings.NewReader(testData))},
		{desc: "onebyte", r: iotest.OneByteReader(strings.NewReader(testData))},
		{desc: "dataerr(onebyte)", r: iotest.DataErrReader(iotest.OneByteReader(strings.NewReader(testData)))},
	}

	for _, tc := range testCases {
		data, err := ioutil.ReadAll(NewReader(receiverFromReader(tc.r)))
		require.NoError(t, err, tc.desc)
		require.Equal(t, testData, string(data), tc.desc)
	}
}

func TestReadSizes(t *testing.T) {
	testData := "Hello this is the test data that will be received. It goes on for a while bla bla bla."
	for n := 1; n < 100; n *= 3 {
		desc := fmt.Sprintf("reads of size %d", n)
		buffer := make([]byte, n)
		result := &bytes.Buffer{}
		reader := &opaqueReader{NewReader(receiverFromReader(strings.NewReader(testData)))}
		_, err := io.CopyBuffer(&opaqueWriter{result}, reader, buffer)
		require.NoError(t, err, desc)
		require.Equal(t, testData, result.String())
	}
}

func receiverFromReader(r io.Reader) func() ([]byte, error) {
	return func() ([]byte, error) {
		data := make([]byte, 10)
		n, err := r.Read(data)
		return data[:n], err
	}
}

// Hide io.WriteTo if it exists
type opaqueReader struct {
	io.Reader
}

// Hide io.ReadFrom if it exists
type opaqueWriter struct {
	io.Writer
}
