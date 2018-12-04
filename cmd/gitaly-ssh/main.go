package main

import (
	"fmt"
	"log"
	"os"

	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/client"
	"google.golang.org/grpc"
)

type packFn func(_ *grpc.ClientConn, _ string) (int32, error)

// GITALY_ADDRESS="tcp://1.2.3.4:9999" or "unix:/var/run/gitaly.sock"
// GITALY_TOKEN="foobar1234"
// GITALY_PAYLOAD="{repo...}"
// GITALY_WD="/path/to/working-directory"
// gitaly-ssh upload-pack <git-garbage-x2>
func main() {
	// < 4 since git throws on 2x garbage here
	if n := len(os.Args); n < 4 {
		// TODO: Errors needs to be sent back some other way... pipes?
		log.Fatalf("invalid number of arguments, expected at least 1, got %d", n-1)
	}

	var packer packFn
	switch os.Args[1] {
	case "upload-pack":
		packer = uploadPack
	case "receive-pack":
		packer = receivePack
	case "upload-archive":
		packer = uploadArchive
	default:
		log.Fatalf("invalid pack command: %q", os.Args[1])
	}

	if wd := os.Getenv("GITALY_WD"); wd != "" {
		if err := os.Chdir(wd); err != nil {
			log.Fatalf("change to : %v", err)
		}
	}

	conn, err := getConnection(os.Getenv("GITALY_ADDRESS"))
	if err != nil {
		log.Fatalf("%s: %v", os.Args[1], err)
	}
	defer conn.Close()

	code, err := packer(conn, os.Getenv("GITALY_PAYLOAD"))
	if err != nil {
		log.Fatalf("%s: %v", os.Args[1], err)
	}

	os.Exit(int(code))
}

func getConnection(url string) (*grpc.ClientConn, error) {
	if url == "" {
		return nil, fmt.Errorf("gitaly address can not be empty")
	}

	return client.Dial(url, dialOpts())
}

func dialOpts() []grpc.DialOption {
	connOpts := client.DefaultDialOpts
	if token := os.Getenv("GITALY_TOKEN"); token != "" {
		connOpts = append(connOpts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentials(token)))
	}

	return connOpts
}
