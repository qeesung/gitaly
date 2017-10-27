package operations

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	testRepo        *pb.Repository
	testRepoPath    string
	gitlabPreHooks  = []string{"pre-receive", "update"}
	gitlabPostHooks = []string{"post-receive"}
	gitlabHooks     []string
)

func init() {
	copy(gitlabHooks, gitlabPreHooks)
	gitlabHooks = append(gitlabHooks, gitlabPostHooks...)
}

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

var rubyServer *rubyserver.Server

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	testRepo = testhelper.TestRepository()

	var err error
	testRepoPath, err = helper.GetRepoPath(testRepo)
	if err != nil {
		log.Fatal(err)
	}

	testhelper.ConfigureRuby()
	rubyServer, err = rubyserver.Start()
	if err != nil {
		log.Fatal(err)
	}
	defer rubyServer.Stop()

	return m.Run()
}

func runOperationServiceServer(t *testing.T) (*grpc.Server, string) {
	grpcServer := testhelper.NewTestGrpcServer(t, nil, nil)
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	pb.RegisterOperationServiceServer(grpcServer, &server{rubyServer})
	reflection.Register(grpcServer)

	go grpcServer.Serve(listener)

	return grpcServer, serverSocketPath
}

func newOperationClient(t *testing.T, serverSocketPath string) (pb.OperationServiceClient, *grpc.ClientConn) {
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

	return pb.NewOperationServiceClient(conn), conn
}

func writeEnvToHook(t *testing.T, hookName string) (string, string) {
	hookOutputTemp, err := ioutil.TempFile("", "")
	require.NoError(t, err)
	require.NoError(t, hookOutputTemp.Close())

	defer os.Remove(hookOutputTemp.Name())

	hookContent := fmt.Sprintf("#!/bin/sh\n/usr/bin/env > %s\n", hookOutputTemp.Name())

	hookPath := path.Join(testRepoPath, "hooks", hookName)
	ioutil.WriteFile(hookPath, []byte(hookContent), 0755)

	return hookPath, hookOutputTemp.Name()
}
