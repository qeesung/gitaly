package gittest

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/pktline"
)

// Pktlinef formats the given formatting string into a pktline.
func Pktlinef(t *testing.T, format string, a ...interface{}) string {
	t.Helper()
	var builder strings.Builder
	_, err := pktline.WriteString(&builder, fmt.Sprintf(format, a...))
	require.NoError(t, err)
	return builder.String()
}

// WritePktlineString writes the pktline-formatted data into the writer.
func WritePktlineString(t *testing.T, writer io.Writer, data string) {
	t.Helper()
	_, err := pktline.WriteString(writer, data)
	require.NoError(t, err)
}

// WritePktlinef formats the given format string and writes the pktline-formatted data into the
// writer.
func WritePktlinef(t *testing.T, writer io.Writer, format string, args ...interface{}) {
	t.Helper()
	_, err := pktline.WriteString(writer, fmt.Sprintf(format, args...))
	require.NoError(t, err)
}

// WritePktlineFlush writes the pktline-formatted flush into the writer.
func WritePktlineFlush(t *testing.T, writer io.Writer) {
	t.Helper()
	require.NoError(t, pktline.WriteFlush(writer))
}

// WritePktlineDelim writes the pktline-formatted delimiter into the writer.
func WritePktlineDelim(t *testing.T, writer io.Writer) {
	t.Helper()
	require.NoError(t, pktline.WriteDelim(writer))
}
