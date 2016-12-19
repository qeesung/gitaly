package client

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net"
	"os"

	"gitlab.com/gitlab-org/gitaly/messaging"
)

type Client struct {
	messagesConn *messaging.MessagesConn
}

func NewClient(serverAddress string) *Client {
	conn, err := net.Dial("tcp", serverAddress)
	if err != nil {
		log.Fatalln(err)
	}

	messagesConn := messaging.NewMessagesConn(conn)
	return &Client{messagesConn}
}

func (client *Client) Run(cmd []string) int {
	rawMsg := messaging.NewCommandMessage(os.Environ(), cmd[0], cmd[1:]...)

	_, err := client.messagesConn.Write(rawMsg)
	if err != nil {
		log.Fatalln(err)
	}

	go streamStdinToServer(client)

	for {
		rawMsg, err := client.messagesConn.Read()

		if err != nil {
			break
		}

		msg, err := messaging.ParseMessage(rawMsg)
		if err != nil {
			break
		}

		switch msg.Type {
		case "stdout":
			os.Stdout.Write(msg.GetOutput().Output)
		case "stderr":
			os.Stderr.Write(msg.GetOutput().Output)
		case "exit":
			return int(msg.GetExit().ExitStatus)
		}
	}

	return 255
}

func (client *Client) Close() {
	client.messagesConn.Close()
}

func streamStdinToServer(client *Client) {
	finished := false
	reader := bufio.NewReader(os.Stdin)

	for {
		buffer := make([]byte, bytes.MinRead)

		n, err := reader.Read(buffer)

		if err == io.EOF {
			finished = true
		}

		if n < bytes.MinRead {
			buffer = buffer[:n]
		}

		rawMsg := messaging.NewInputMessage(buffer)
		client.messagesConn.Write(rawMsg)

		if finished {
			return
		}
	}
}
