package notifications

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
)

var (
	serverSocketPath = testhelper.GetTemporaryGitalySocketFileName()
	testRepo         *pb.Repository
)

func TestMain(m *testing.M) {
	testRepo = testhelper.TestRepository()

	os.Exit(func() int {
		return m.Run()
	}())
}

func TestSuccessfulPostReceive(t *testing.T) {
	server := runNotificationsServer(t)
	defer server.Stop()

	client := newNotificationsClient(t)
	rpcRequest := &pb.PostReceiveRequest{Repository: testRepo}

	_, err := client.PostReceive(context.Background(), rpcRequest)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEmptyPostReceiveRequest(t *testing.T) {
	server := runNotificationsServer(t)
	defer server.Stop()

	client := newNotificationsClient(t)
	rpcRequest := &pb.PostReceiveRequest{}

	_, err := client.PostReceive(context.Background(), rpcRequest)
	testhelper.AssertGrpcError(t, err, codes.InvalidArgument, "")
}

func runNotificationsServer(t *testing.T) *grpc.Server {
	server := grpc.NewServer()
	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterNotificationsServer(server, NewServer())
	reflection.Register(server)

	go server.Serve(listener)

	return server
}

func newNotificationsClient(t *testing.T) pb.NotificationsClient {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, _ time.Duration) (net.Conn, error) {
			return net.Dial("unix", addr)
		}),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return pb.NewNotificationsClient(conn)
}
