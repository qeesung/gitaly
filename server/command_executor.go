package server

import (
	"bytes"
	"io"
	"log"
	"os/exec"
	"syscall"

	"gitlab.com/gitlab-org/gitaly/messaging"
)

func CommandExecutor(chans *commChans) {
	rawMsg, ok := <-chans.inChan
	if !ok {
		return
	}

	msg, err := messaging.ParseMessage(rawMsg)
	if err != nil {
		return
	}
	if msg.Type != "command" {
		return
	}

	runCommand(chans, msg.GetCommand())
}

func runCommand(chans *commChans, commandMsg *messaging.Command) {
	name := commandMsg.Name
	args := commandMsg.Args

	log.Println("Executing command:", name, "with args", args)

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()

	go streamOut("stdout", stdoutReader, chans)
	go streamOut("stderr", stderrReader, chans)
	go streamIn(stdinWriter, chans)

	cmd := exec.Command(name, args...)

	// Start the command in its own process group (nice for signalling)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = commandMsg.Environ
	cmd.Stdin = stdinReader
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	err := cmd.Run()

	if err != nil {
		exitStatus := int32(extractExitStatusFromError(err.(*exec.ExitError)))
		chans.outChan <- messaging.NewExitMessage(exitStatus)
		return
	}

	chans.outChan <- messaging.NewExitMessage(0)
}

func extractExitStatusFromError(err *exec.ExitError) int {
	processState := err.ProcessState
	status := processState.Sys().(syscall.WaitStatus)

	if status.Exited() {
		return status.ExitStatus()
	}

	return 255
}

func streamOut(streamName string, streamPipe io.Reader, chans *commChans) {
	// TODO: Move buffer out of the loop and use defer instead of finished
	finished := false

	for {
		buffer := make([]byte, bytes.MinRead)

		n, err := streamPipe.Read(buffer)
		if err == io.EOF {
			finished = true
		}

		if n < bytes.MinRead {
			buffer = buffer[:n]
		}

		chans.outChan <- messaging.NewOutputMessage(streamName, buffer)

		if finished {
			return
		}
	}
}

func streamIn(streamPipe *io.PipeWriter, chans *commChans) {
	defer streamPipe.Close()

	for {
		rawMsg, ok := <-chans.inChan
		if !ok {
			return
		}

		msg, err := messaging.ParseMessage(rawMsg)
		if msg.Type != "stdin" {
			continue
		}

		stdin := msg.GetInput().Stdin
		if len(stdin) == 0 {
			return
		}

		_, err = streamPipe.Write(stdin)
		if err != nil {
			return
		}
	}
}
