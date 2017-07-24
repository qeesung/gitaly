package helper

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestNewCommand_Env(t *testing.T) {
	oldTZ := os.Getenv("TZ")
	defer os.Setenv("TZ", oldTZ)

	os.Setenv("TZ", "foobar")

	buff := &bytes.Buffer{}
	cmd, err := NewCommand(context.Background(), exec.Command("env"), nil, buff, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	found := false
	split := bytes.Split(buff.Bytes(), []byte("\n"))
	for _, line := range split {
		if bytes.Compare(line, []byte("TZ=foobar")) == 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("TZ not set to `foobar`")
	}
}
