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
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/rubyserver/balancer"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
	"gitlab.com/gitlab-org/gitaly/v15/internal/supervisor"
	"gitlab.com/gitlab-org/gitaly/v15/internal/version"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	grpccorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	grpctracing "gitlab.com/gitlab-org/labkit/tracing/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ConnectTimeout is the timeout for establishing a connection to the gitaly-ruby process.
var ConnectTimeout = 40 * time.Second

func init() {
	timeout, err := env.GetInt("GITALY_RUBY_CONNECT_TIMEOUT", 0)
	if err == nil && timeout > 0 {
		ConnectTimeout = time.Duration(timeout) * time.Second
	}
}

func setupEnv(cfg config.Cfg, gitCmdFactory git.CommandFactory) ([]string, error) {
	// Ideally, we'd pass in the RPC context to the Git command factory such that we can
	// properly use feature flags to switch between different execution environments. But the
	// Ruby server is precreated and thus cannot use feature flags here. So for now, we have to
	// live with the fact that we cannot use feature flags for it.
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	gitExecEnv := gitCmdFactory.GetExecutionEnvironment(ctx)
	hooksPath := gitCmdFactory.HooksPath(ctx)

	environment := append(
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
	environment = append(environment, gitExecEnv.EnvironmentVariables...)
	environment = append(environment, env.AllowedRubyEnvironment(os.Environ())...)

	gitConfig, err := gitCmdFactory.SidecarGitConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting Git configuration: %w", err)
	}
	environment = append(environment, git.ConfigPairsToGitEnvironment(gitConfig)...)

	if dsn := cfg.Logging.RubySentryDSN; dsn != "" {
		environment = append(environment, "SENTRY_DSN="+dsn)
	}

	if sentryEnvironment := cfg.Logging.Sentry.Environment; sentryEnvironment != "" {
		environment = append(environment, "SENTRY_ENVIRONMENT="+sentryEnvironment)
	}

	return environment, nil
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
	gitconfigDir  string
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
			w.Process.Stop()
			w.stopMonitor()
		}

		if s.gitconfigDir != "" {
			_ = os.RemoveAll(s.gitconfigDir)
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

	// Both Omnibus and CNG set up the gitconfig in a non-default location. This means that they
	// need to tell us where to find it, which is done via the Rugged config search path. In
	// fact though, this configuration really only contains a single entry that is of importance
	// to us in the context of Rugged, which is `core.fsyncObjectFiles`: if not set, then we may
	// fail to persist objects correctly and thus corrupt the repository. We don't care about
	// anything else nowadays anymore because most of the functionality was stripped out of the
	// sidecar.
	//
	// Because we only care about a single option, and because that option is in fact mandatory
	// or we may end up with corrupted data, we want to get rid of this configuration. Rugged
	// doesn't give us any way to force-enable fsyncing though except if we write it to a file.
	// Consequentially, we'll have to inject our own gitconfig into Rugged that enables this
	// config. And that's exactly what the following block does: if we detect that the distro
	// isn't telling us where to find the Rugged configuration, we write our own config. This is
	// required so that we can phase out support of the gitconfig in these distributions.
	//
	// This is transitory until either the sidecar goes away or the upstream pull request is
	// released (https://github.com/libgit2/rugged/pull/918).
	if cfg.Ruby.RuggedGitConfigSearchPath == "" {
		gitconfigDir := filepath.Join(cfg.RuntimeDir, "ruby-gitconfig")
		if err := os.Mkdir(gitconfigDir, 0o777); err != nil {
			return fmt.Errorf("creating gitconfig dir: %w", err)
		}

		// This file must be called `gitconfig` given that we pretend it's the system-level
		// Git configuration. Otherwise, Rugged wouldn't find it.
		if err := os.WriteFile(filepath.Join(gitconfigDir, "gitconfig"), []byte(
			"[core]\n\tfsyncObjectFiles = true\n",
		), 0o666); err != nil {
			return fmt.Errorf("writing gitconfig: %w", err)
		}

		cfg.Ruby.RuggedGitConfigSearchPath = gitconfigDir
		s.gitconfigDir = gitconfigDir
	}

	env, err := setupEnv(cfg, s.gitCmdFactory)
	if err != nil {
		return fmt.Errorf("setting up sidecar environment: %w", err)
	}

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
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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
