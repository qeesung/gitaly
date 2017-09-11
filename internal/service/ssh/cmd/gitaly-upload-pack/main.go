package main

import (
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/net/context"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/client"
)

func main() {
	if !(len(os.Args) >= 3 && strings.HasPrefix(os.Args[2], "git-upload-pack")) {
		log.Fatalf("Not a valid command")
	}

	addr := os.Getenv("GITALY_SOCKET")
	if len(addr) == 0 {
		log.Fatalf("GITALY_SOCKET not set")
	}

	conn, err := client.Dial(addr, client.DefaultDialOpts)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	defer conn.Close()

	req := &pb.SSHUploadPackRequest{
		Repository: &pb.Repository{
			RelativePath: os.Getenv("GL_RELATIVEPATH"),
			StorageName:  os.Getenv("GL_STORAGENAME"),
		},
	}

	// Use GL_CONFIG_OPTIONS instead of GIT_CONFIG_PARAMETERS because the latter
	// would be swallowed by the client git process
	gitConfig := os.Getenv("GL_CONFIG_OPTIONS")

	if len(gitConfig) > 0 {
		configs := strings.Split(gitConfig, " ")

		req.GitConfigOptions = append(req.GitConfigOptions, configs...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := client.UploadPack(ctx, conn, os.Stdin, os.Stdout, os.Stderr, req)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	os.Exit(int(code))
}
