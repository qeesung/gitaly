package server

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/messaging"
)

func TestRunningCommandSuccessfully(t *testing.T) {
	chans := newCommChans()
	defer chans.Close()

	go CommandExecutor(chans)

	chans.inChan <- messaging.NewCommandMessage([]string{}, "ls", "-hal")
	chans.inChan <- messaging.NewInputMessage([]byte{})

	for {
		rawMsg := <-chans.outChan
		msg, _ := messaging.ParseMessage(rawMsg)

		switch msg.Type {
		case "exit":
			exit_status := msg.GetExit().ExitStatus
			if exit_status != 0 {
				t.Error("Expected exit status of 0, found %v", exit_status)
			}
			return
		}
	}
}

func TestRunningCommandUnsuccessfully(t *testing.T) {
	chans := newCommChans()
	defer chans.Close()

	go CommandExecutor(chans)

	chans.inChan <- messaging.NewCommandMessage([]string{}, "ls", "/file-that-does-not-exist")
	chans.inChan <- messaging.NewInputMessage([]byte{})

	for {
		rawMsg := <-chans.outChan
		msg, _ := messaging.ParseMessage(rawMsg)

		switch msg.Type {
		case "exit":
			exit_status := msg.GetExit().ExitStatus
			if exit_status == 0 {
				t.Error("Expected a failure exit status, got 0")
			}
			return
		}
	}
}
