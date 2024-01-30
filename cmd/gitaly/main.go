package main

import (
	"log"
	"os"

	cli "gitlab.com/gitlab-org/gitaly/v16/internal/cli/gitaly"
)

func main() {
	// testing the pipeline
	if err := cli.NewApp().Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
