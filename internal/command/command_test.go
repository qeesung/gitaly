package command

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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

func TestNewCommandTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer func(ch chan struct{}, t time.Duration) {
		spawnTokens = ch
		spawnConfig.Timeout = t
	}(spawnTokens, spawnConfig.Timeout)

	// This unbuffered channel will behave like a full/blocked buffered channel.
	spawnTokens = make(chan struct{})
	// Speed up the test by lowering the timeout
	spawnTimeout := 200 * time.Millisecond
	spawnConfig.Timeout = spawnTimeout

	testDeadline := time.After(1 * time.Second)
	tick := time.After(spawnTimeout / 2)

	errCh := make(chan error)
	go func() {
		_, err := New(ctx, exec.Command("true"), nil, nil, nil)
		errCh <- err
	}()

	var err error
	timePassed := false

wait:
	for {
		select {
		case err = <-errCh:
			break wait
		case <-tick:
			timePassed = true
		case <-testDeadline:
			t.Fatal("test timed out")
		}
	}

	require.True(t, timePassed, "time must have passed")
	require.Error(t, err)

	_, ok := err.(spawnTimeoutError)
	require.True(t, ok, "type of error should be spawnTimeoutError")
}
