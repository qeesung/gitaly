package rubyserver

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/supervisor"
	"gitlab.com/gitlab-org/gitaly/streamio"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	repoPathHeader = "gitaly-repo-path"
)

var (
	socketDir string

	lazyInit sync.Once

	// ConnectTimeout is the timeout for establishing a connection to the gitaly-ruby process.
	ConnectTimeout = 20 * time.Second
)

func init() {
	timeout64, err := strconv.ParseInt(os.Getenv("GITALY_RUBY_CONNECT_TIMEOUT"), 10, 32)
	if err == nil && timeout64 > 0 {
		ConnectTimeout = time.Duration(timeout64) * time.Second
	}
}

func prepareSocketPath() {
	// The socket path must be short-ish because listen(2) fails on long
	// socket paths. We hope/expect that ioutil.TempDir creates a directory
	// that is not too deep. We need a directory, not a tempfile, because we
	// will later want to set its permissions to 0700. The permission change
	// is done in the Ruby child process.
	var err error
	socketDir, err = ioutil.TempDir("", "gitaly-ruby")
	if err != nil {
		log.Fatalf("create ruby server socket directory: %v", err)
	}
}

func socketPath() string {
	if socketDir == "" {
		panic("socketDir is not set")
	}

	return path.Join(filepath.Clean(socketDir), "socket")
}

// Server represents a gitaly-ruby helper process.
type Server struct {
	*supervisor.Process
	clientConnMu sync.RWMutex
	clientConn   *grpc.ClientConn
}

// Stop shuts down the gitaly-ruby helper process and cleans up resources.
func (s *Server) Stop() {
	if s != nil {
		s.clientConnMu.RLock()
		defer s.clientConnMu.RUnlock()
		if s.clientConn != nil {
			s.clientConn.Close()
		}

		if s.Process != nil {
			s.Process.Stop()
		}
	}

	if socketDir != "" {
		os.RemoveAll(socketDir)
	}
}

// Start spawns the Ruby server.
func Start() (*Server, error) {
	lazyInit.Do(prepareSocketPath)

	cfg := config.Config
	env := []string{
		"GITALY_RUBY_GIT_BIN_PATH=" + command.GitPath(),
		fmt.Sprintf("GITALY_RUBY_WRITE_BUFFER_SIZE=%d", streamio.WriteBufferSize),
		"GITALY_RUBY_GITLAB_SHELL_PATH=" + cfg.GitlabShell.Dir,
	}

	args := []string{"bundle", "exec", "bin/gitaly-ruby", fmt.Sprintf("%d", os.Getpid()), socketPath()}

	p, err := supervisor.New("gitaly-ruby", append(os.Environ(), env...), args, cfg.Ruby.Dir)
	return &Server{Process: p}, err
}

// CommitServiceClient returns a CommitServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) CommitServiceClient(ctx context.Context) (pb.CommitServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return pb.NewCommitServiceClient(conn), err
}

// DiffServiceClient returns a DiffServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) DiffServiceClient(ctx context.Context) (pb.DiffServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return pb.NewDiffServiceClient(conn), err
}

// RefServiceClient returns a RefServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) RefServiceClient(ctx context.Context) (pb.RefServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return pb.NewRefServiceClient(conn), err
}

// OperationServiceClient returns a OperationServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) OperationServiceClient(ctx context.Context) (pb.OperationServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return pb.NewOperationServiceClient(conn), err
}

// RepositoryServiceClient returns a RefServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) RepositoryServiceClient(ctx context.Context) (pb.RepositoryServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return pb.NewRepositoryServiceClient(conn), err
}

func (s *Server) getConnection(ctx context.Context) (*grpc.ClientConn, error) {
	s.clientConnMu.RLock()
	conn := s.clientConn
	s.clientConnMu.RUnlock()

	if conn != nil {
		return conn, nil
	}

	return s.createConnection(ctx)
}

func (s *Server) createConnection(ctx context.Context) (*grpc.ClientConn, error) {
	s.clientConnMu.Lock()
	defer s.clientConnMu.Unlock()

	if conn := s.clientConn; conn != nil {
		return conn, nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, socketPath(), dialOptions()...)
	if err != nil {
		return nil, err
	}

	s.clientConn = conn
	return s.clientConn, nil
}

func dialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithBlock(), // With this we get retries. Without, connections fail fast.
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	}
}

// SetHeaders adds headers that tell gitaly-ruby the full path to the repository.
func SetHeaders(ctx context.Context, repo *pb.Repository) (context.Context, error) {
	repoPath, err := helper.GetPath(repo)
	if err != nil {
		return nil, err
	}

	newCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(repoPathHeader, repoPath))
	return newCtx, nil
}

// Proxy calls recvSend until it receives an error. The error is returned
// to the caller unless it is io.EOF.
func Proxy(recvSend func() error) (err error) {
	for err == nil {
		err = recvSend()
	}

	if err == io.EOF {
		err = nil
	}
	return err
}
