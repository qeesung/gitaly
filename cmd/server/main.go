package main

import (
	"os"
	"os/signal"
	"syscall"

	serv "gitlab.com/gitlab-org/gitaly/server"
)

func main() {
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	server := serv.NewServer()
	go func() {
		server.Serve("0.0.0.0:6666", serv.CommandExecutor)
	}()

	select {
	case <-ch:
		server.Stop()
	}
}
