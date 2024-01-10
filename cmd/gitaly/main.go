package main

import (
	"fmt"
	"log"
	"os"

	cli "gitlab.com/gitlab-org/gitaly/v16/internal/cli/gitaly"
)

func main() {
	if err := cli.NewApp().Run(os.Args); err != nil {
		log.Fatal(err)
	}

	fmt.Println("hello")
}
