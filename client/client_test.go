package client

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"path"
	"testing"
	"time"

	serv "gitlab.com/gitlab-org/gitaly/server"
)

const serverAddress = "127.0.0.1:6667"
const testRepo = "group/test.git"
const testRepoRoot = "testdata/data"

var origStdout = os.Stdout
var origStderr = os.Stderr

func TestMain(m *testing.M) {
	source := "https://gitlab.com/gitlab-org/gitlab-test.git"
	clonePath := path.Join(testRepoRoot, testRepo)
	if _, err := os.Stat(clonePath); err != nil {
		testCmd := exec.Command("git", "clone", "--bare", source, clonePath)
		testCmd.Stdout = os.Stdout
		testCmd.Stderr = os.Stderr
		if err := testCmd.Run(); err != nil {
			log.Printf("Test setup: failed to run %v", testCmd)
			os.Exit(-1)
		}
	}
	server := serv.NewServer()

	go server.Serve(serverAddress, serv.CommandExecutor)
	defer server.Stop()

	time.Sleep(10 * time.Millisecond)
	os.Exit(m.Run())
}

func TestRunningGitCommandSuccessfully(t *testing.T) {
	client := NewClient(serverAddress)
	defer client.Close()

	stdout, _ := redirectOutputStreams()
	exitStatus := client.Run([]string{
		"git",
		"--git-dir",
		path.Join(testRepoRoot, testRepo),
		"rev-list",
		"--count",
		"b83d6e391c",
	})
	restoreOutputStreams()

	expectedExitStatus := 0
	if exitStatus != expectedExitStatus {
		t.Fatalf("Expected response exit status to equal %d, got %d", expectedExitStatus, exitStatus)
	}

	expectedStdout := []byte("37\n")
	gotStdout := make([]byte, len(expectedStdout))
	stdout.Read(gotStdout)
	if !bytes.Equal(gotStdout, expectedStdout) {
		t.Fatalf("Expected response stdout to be \"%s\", got \"%s\"", expectedStdout, gotStdout)
	}
}

func TestRunningGitCommandUnsuccessfully(t *testing.T) {
	client := NewClient(serverAddress)
	defer client.Close()

	_, stderr := redirectOutputStreams()
	exitStatus := client.Run([]string{
		"git",
		"--git-dir",
		path.Join(testRepoRoot, testRepo),
		"rev-list",
		"--count",
		"babecafe",
	})
	restoreOutputStreams()

	expectedExitStatus := 128
	if exitStatus != expectedExitStatus {
		t.Fatalf("Expected response exit status to equal %d, got %d", expectedExitStatus, exitStatus)
	}

	expectedStderr := []byte("fatal: ambiguous argument 'babecafe': unknown revision or path not in the working tree.")
	gotStderr := make([]byte, len(expectedStderr))
	stderr.Read(gotStderr)
	if !bytes.Contains(gotStderr, expectedStderr) {
		t.Fatalf("Expected stderr to contain \"%s\", found none in \"%s\"", expectedStderr, gotStderr)
	}
}

func redirectOutputStreams() (*os.File, *os.File) {
	stdoutReader, stdoutWriter, _ := os.Pipe()
	stderrReader, stderrWriter, _ := os.Pipe()

	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter

	return stdoutReader, stderrReader
}

func restoreOutputStreams() {
	os.Stdout = origStdout
	os.Stderr = origStderr
}
