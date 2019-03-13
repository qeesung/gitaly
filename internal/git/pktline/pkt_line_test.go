package pktline

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	largestString = strings.Repeat("z", 0xffff-4)
)

func TestScanner(t *testing.T) {
	largestPacket := "ffff" + largestString
	testCases := []struct {
		desc string
		in   string
		out  []string
		fail bool
	}{
		{
			desc: "happy path",
			in:   "0010hello world!000000010010hello world!",
			out:  []string{"0010hello world!", "0000", "0001", "0010hello world!"},
		},
		{
			desc: "large input",
			in:   "0010hello world!0000" + largestPacket + "0000",
			out:  []string{"0010hello world!", "0000", largestPacket, "0000"},
		},
		{
			desc: "missing byte middle",
			in:   "0010hello world!00000010010hello world!",
			out:  []string{"0010hello world!", "0000", "0010010hello wor"},
			fail: true,
		},
		{
			desc: "unfinished prefix",
			in:   "0010hello world!000",
			out:  []string{"0010hello world!"},
			fail: true,
		},
		{
			desc: "short read in data, only prefix",
			in:   "0010hello world!0005",
			out:  []string{"0010hello world!"},
			fail: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			scanner := NewScanner(strings.NewReader(tc.in))
			var output []string
			for scanner.Scan() {
				output = append(output, scanner.Text())
			}

			if tc.fail {
				require.Error(t, scanner.Err())

			} else {
				require.NoError(t, scanner.Err())
			}

			require.Equal(t, tc.out, output)
		})
	}
}

func TestData(t *testing.T) {
	testCases := []struct {
		in  string
		out string
	}{
		{in: "0008abcd", out: "abcd"},
		{in: "invalid packet", out: "lid packet"},
		{in: "0005wrong length prefix", out: "wrong length prefix"},
		{in: "0000", out: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			require.Equal(t, tc.out, string(Data([]byte(tc.in))))
		})
	}
}

func TestIsFlush(t *testing.T) {
	testCases := []struct {
		in    string
		flush bool
	}{
		{in: "0008abcd", flush: false},
		{in: "invalid packet", flush: false},
		{in: "0000", flush: true},
		{in: "0001", flush: false},
	}

	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			require.Equal(t, tc.flush, IsFlush([]byte(tc.in)))
		})
	}
}

func TestWriteString(t *testing.T) {
	testCases := []struct {
		desc string
		in   string
		out  string
		fail bool
	}{
		{
			desc: "empty string",
			in:   "",
			out:  "0004",
		},
		{
			desc: "small string",
			in:   "hello world!",
			out:  "0010hello world!",
		},
		{
			desc: "largest possible string",
			in:   largestString,
			out:  "ffff" + largestString,
		},
		{
			desc: "string that is too large",
			in:   "x" + largestString,
			fail: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			w := &bytes.Buffer{}
			n, err := WriteString(w, tc.in)

			if tc.fail {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, len(tc.in), n, "number of bytes written reported by WriteString")

			require.Equal(t, tc.out, w.String())
		})
	}
}

func TestWriteFlush(t *testing.T) {
	w := &bytes.Buffer{}
	require.NoError(t, WriteFlush(w))
	require.Equal(t, "0000", w.String())
}
