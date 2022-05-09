package rubyserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	grpcmw "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver/balancer"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/helper/env"
	"gitlab.com/gitlab-org/gitaly/internal/supervisor"
	"gitlab.com/gitlab-org/gitaly/internal/version"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
	grpccorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	grpctracing "gitlab.com/gitlab-org/labkit/tracing/grpc"
	"google.golang.org/grpc"
)

// ConnectTimeout is the timeout for establishing a connection to the gitaly-ruby process.
var ConnectTimeout = 40 * time.Second

func init() {
	timeout, err := env.GetInt("GITALY_RUBY_CONNECT_TIMEOUT", 0)
	if err == nil && timeout > 0 {
		ConnectTimeout = time.Duration(timeout) * time.Second
	}
}

func setupEnv(cfg config.Cfg, gitCmdFactory git.CommandFactory) []string {
	// Ideally, we'd pass in the RPC context to the Git command factory such that we can
	// properly use feature flags to switch between different execution environments. But the
	// Ruby server is precreated and thus cannot use feature flags here. So for now, we have to
	// live with the fact that we cannot use feature flags for it.
	gitExecEnv := gitCmdFactory.GetExecutionEnvironment(context.TODO())
	// The same remark exists with our hooks path.
	hooksPath := gitCmdFactory.HooksPath(context.TODO())

	env := append(
		command.AllowedEnvironment(os.Environ()),
		"GITALY_LOG_DIR="+cfg.Logging.Dir,
		"GITALY_RUBY_GIT_BIN_PATH="+gitExecEnv.BinaryPath,
		fmt.Sprintf("GITALY_RUBY_WRITE_BUFFER_SIZE=%d", streamio.WriteBufferSize),
		fmt.Sprintf("GITALY_RUBY_MAX_COMMIT_OR_TAG_MESSAGE_SIZE=%d", helper.MaxCommitOrTagMessageSize),
		"GITALY_RUBY_GITALY_BIN_DIR="+cfg.BinDir,
		"GITALY_VERSION="+version.GetVersion(),
		"GITALY_GIT_HOOKS_DIR="+hooksPath,
		"GITALY_SOCKET="+cfg.InternalSocketPath(),
		"GITALY_TOKEN="+cfg.Auth.Token,
		"GITALY_RUGGED_GIT_CONFIG_SEARCH_PATH="+cfg.Ruby.RuggedGitConfigSearchPath,
	)
	env = append(env, command.GitEnv...)
	env = append(env, gitExecEnv.EnvironmentVariables...)
	env = appendEnvIfSet(env, "BUNDLE_PATH")
	env = appendEnvIfSet(env, "BUNDLE_USER_CONFIG")
	env = appendEnvIfSet(env, "GEM_HOME")

	if dsn := cfg.Logging.RubySentryDSN; dsn != "" {
		env = append(env, "SENTRY_DSN="+dsn)
	}

	if sentryEnvironment := cfg.Logging.Sentry.Environment; sentryEnvironment != "" {
		env = append(env, "SENTRY_ENVIRONMENT="+sentryEnvironment)
	}

	return env
}

func appendEnvIfSet(envvars []string, key string) []string {
	if value, ok := os.LookupEnv(key); ok {
		envvars = append(envvars, fmt.Sprintf("%s=%s", key, value))
	}
	return envvars
}

// Server represents a gitaly-ruby helper process.
type Server struct {
	cfg           config.Cfg
	gitCmdFactory git.CommandFactory
	startOnce     sync.Once
	startErr      error
	workers       []*worker
	clientConnMu  sync.Mutex
	clientConn    *grpc.ClientConn
}

// New returns a new instance of the server.
func New(cfg config.Cfg, gitCmdFactory git.CommandFactory) *Server {
	return &Server{cfg: cfg, gitCmdFactory: gitCmdFactory}
}

// Stop shuts down the gitaly-ruby helper process and cleans up resources.
func (s *Server) Stop() {
	if s != nil {
		s.clientConnMu.Lock()
		defer s.clientConnMu.Unlock()
		if s.clientConn != nil {
			s.clientConn.Close()
		}

		for _, w := range s.workers {
			w.stopMonitor()
			w.Process.Stop()
		}
	}
}

// Start spawns the Ruby server.
func (s *Server) Start() error {
	s.startOnce.Do(func() { s.startErr = s.start() })
	return s.startErr
}

func (s *Server) start() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg := s.cfg
	env := setupEnv(cfg, s.gitCmdFactory)

	gitalyRuby := filepath.Join(cfg.Ruby.Dir, "bin", "gitaly-ruby")

	numWorkers := cfg.Ruby.NumWorkers
	balancer.ConfigureBuilder(numWorkers, 0, time.Now)

	svConfig, err := supervisor.NewConfigFromEnv()
	if err != nil {
		return fmt.Errorf("get supervisor configuration: %w", err)
	}

	for i := 0; i < numWorkers; i++ {
		name := fmt.Sprintf("gitaly-ruby.%d", i)
		socketPath := filepath.Join(cfg.InternalSocketDir(), fmt.Sprintf("ruby.%d", i))

		// Use 'ruby-cd' to make sure gitaly-ruby has the same working directory
		// as the current process. This is a hack to sort-of support relative
		// Unix socket paths.
		args := []string{"bundle", "exec", "bin/ruby-cd", wd, gitalyRuby, strconv.Itoa(os.Getpid()), socketPath}

		events := make(chan supervisor.Event)
		check := func() error { return ping(socketPath) }
		p, err := supervisor.New(svConfig, name, env, args, cfg.Ruby.Dir, cfg.Ruby.MaxRSS, events, check)
		if err != nil {
			return err
		}

		restartDelay := cfg.Ruby.RestartDelay.Duration()
		gracefulRestartTimeout := cfg.Ruby.GracefulRestartTimeout.Duration()
		s.workers = append(s.workers, newWorker(p, socketPath, restartDelay, gracefulRestartTimeout, events, false))
	}

	return nil
}

// CommitServiceClient returns a CommitServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) CommitServiceClient(ctx context.Context) (gitalypb.CommitServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return gitalypb.NewCommitServiceClient(conn), err
}

// DiffServiceClient returns a DiffServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) DiffServiceClient(ctx context.Context) (gitalypb.DiffServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return gitalypb.NewDiffServiceClient(conn), err
}

// RefServiceClient returns a RefServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) RefServiceClient(ctx context.Context) (gitalypb.RefServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return gitalypb.NewRefServiceClient(conn), err
}

// OperationServiceClient returns a OperationServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) OperationServiceClient(ctx context.Context) (gitalypb.OperationServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return gitalypb.NewOperationServiceClient(conn), err
}

// RepositoryServiceClient returns a RefServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) RepositoryServiceClient(ctx context.Context) (gitalypb.RepositoryServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return gitalypb.NewRepositoryServiceClient(conn), err
}

// WikiServiceClient returns a WikiServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) WikiServiceClient(ctx context.Context) (gitalypb.WikiServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return gitalypb.NewWikiServiceClient(conn), err
}

// RemoteServiceClient returns a RemoteServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) RemoteServiceClient(ctx context.Context) (gitalypb.RemoteServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return gitalypb.NewRemoteServiceClient(conn), err
}

// BlobServiceClient returns a BlobServiceClient instance that is
// configured to connect to the running Ruby server. This assumes Start()
// has been called already.
func (s *Server) BlobServiceClient(ctx context.Context) (gitalypb.BlobServiceClient, error) {
	conn, err := s.getConnection(ctx)
	return gitalypb.NewBlobServiceClient(conn), err
}

func (s *Server) getConnection(ctx context.Context) (*grpc.ClientConn, error) {
	s.clientConnMu.Lock()
	conn := s.clientConn
	s.clientConnMu.Unlock()

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

	conn, err := grpc.DialContext(dialCtx, balancer.Scheme+":///gitaly-ruby", dialOptions()...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gitaly-ruby worker: %v", err)
	}

	s.clientConn = conn
	return s.clientConn, nil
}

func dialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithBlock(), // With this we get retries. Without, connections fail fast.
		grpc.WithInsecure(),
		// Use a custom dialer to ensure that we don't experience
		// issues in environments that have proxy configurations
		// https://gitlab.com/gitlab-org/gitaly/merge_requests/1072#note_140408512
		grpc.WithContextDialer(func(ctx context.Context, addr string) (conn net.Conn, err error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", addr)
		}),
		grpc.WithUnaryInterceptor(
			grpcmw.ChainUnaryClient(
				grpcprometheus.UnaryClientInterceptor,
				grpctracing.UnaryClientTracingInterceptor(),
				grpccorrelation.UnaryClientCorrelationInterceptor(),
			),
		),
		grpc.WithStreamInterceptor(
			grpcmw.ChainStreamClient(
				grpcprometheus.StreamClientInterceptor,
				grpctracing.StreamClientTracingInterceptor(),
				grpccorrelation.StreamClientCorrelationInterceptor(),
			),
		),
	}
}
