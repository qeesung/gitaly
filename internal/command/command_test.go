package command

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCommandTZEnv(t *testing.T) {
	oldTZ := os.Getenv("TZ")
	defer os.Setenv("TZ", oldTZ)

	os.Setenv("TZ", "foobar")

	buff := &bytes.Buffer{}
	cmd, err := New(context.Background(), exec.Command("env"), nil, buff, nil)

	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	require.Contains(t, strings.Split(buff.String(), "\n"), "TZ=foobar")
}

func TestNewCommandExtraEnv(t *testing.T) {
	extraVar := "FOOBAR=123456"
	buff := &bytes.Buffer{}
	cmd, err := New(context.Background(), exec.Command("/usr/bin/env"), nil, buff, nil, extraVar)

	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	require.Contains(t, strings.Split(buff.String(), "\n"), extraVar)
}
