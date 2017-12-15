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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	oldTZ := os.Getenv("TZ")
	defer os.Setenv("TZ", oldTZ)

	os.Setenv("TZ", "foobar")

	buff := &bytes.Buffer{}
	cmd, err := New(ctx, exec.Command("env"), nil, buff, nil)

	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	require.Contains(t, strings.Split(buff.String(), "\n"), "TZ=foobar")
}

func TestNewCommandExtraEnv(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	extraVar := "FOOBAR=123456"
	buff := &bytes.Buffer{}
	cmd, err := New(ctx, exec.Command("/usr/bin/env"), nil, buff, nil, extraVar)

	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	require.Contains(t, strings.Split(buff.String(), "\n"), extraVar)
}

func TestRejectEmptyContextDone(t *testing.T) {
	defer func() {
		p := recover()
		if p == nil {
			t.Error("expected panic, got none")
			return
		}

		if _, ok := p.(contextWithoutDonePanic); !ok {
			panic(p)
		}
	}()

	New(context.Background(), exec.Command("true"), nil, nil, nil)
}
