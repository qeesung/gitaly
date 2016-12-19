package main

import (
	"os"

	"gitlab.com/gitlab-org/gitaly/client"
)

func main() {
	client := client.NewClient("127.0.0.1:6666")
	defer client.Close()

	os.Args[0] = "git"

	exitStatus := client.Run(os.Args)
	os.Exit(exitStatus)
}
