package server

import (
	"fmt"
	"net"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/messaging"
)

func TestServerStandingUp(t *testing.T) {
	server := NewServer()
	address := "127.0.0.1:6666"

	go server.Serve(address, func(chans *commChans) {
		a := (<-chans.inChan)
		chans.outChan <- a
	})
	defer server.Stop()

	// Give server a little time to start listening for connections
	time.Sleep(10 * time.Millisecond)

	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	messagesConn := messaging.NewMessagesConn(conn)

	if _, err := messagesConn.Write([]byte("hola hola!")); err != nil {
		t.Error(err)
	}

	buffer, err := messagesConn.Read()
	if err != nil {
		t.Error(err)
	}
	messagesConn.Close()

	fmt.Println("Received from server:", string(buffer))
}
