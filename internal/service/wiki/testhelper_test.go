package wiki

import (
	"net"
	"os"
	"path"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	wikiRepo     *pb.Repository
	wikiRepoPath string
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

var rubyServer *rubyserver.Server

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	testhelper.ConfigureTestStorage()
	storagePath := testhelper.GitlabTestStoragePath()
	wikiRepoPath = path.Join(storagePath, "wiki-test.git")

	testhelper.MustRunCommand(nil, nil, "git", "init", "--bare", wikiRepoPath)
	defer os.RemoveAll(wikiRepoPath)

	wikiRepo = &pb.Repository{
		StorageName:  "default",
		RelativePath: "wiki-test.git",
	}

	var err error
	testhelper.ConfigureRuby()
	rubyServer, err = rubyserver.Start()
	if err != nil {
		log.Fatal(err)
	}
	defer rubyServer.Stop()

	return m.Run()
}

func runWikiServiceServer(t *testing.T) (*grpc.Server, string) {
	grpcServer := testhelper.NewTestGrpcServer(t, nil, nil)
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterWikiServiceServer(grpcServer, &server{rubyServer})
	reflection.Register(grpcServer)

	go grpcServer.Serve(listener)

	return grpcServer, serverSocketPath
}

func newWikiClient(t *testing.T, serverSocketPath string) (pb.WikiServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return pb.NewWikiServiceClient(conn), conn
}
