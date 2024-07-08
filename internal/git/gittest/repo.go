package gittest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v16/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	// GlRepository is the default repository name for newly created test
	// repos.
	GlRepository = "project-1"
	// GlProjectPath is the default project path for newly created test
	// repos.
	GlProjectPath = "gitlab-org/gitlab-test"
)

// InitRepoDir creates a temporary directory for a repo, without initializing it
func InitRepoDir(tb testing.TB, storagePath, relativePath string) *gitalypb.Repository {
	repoPath := filepath.Join(storagePath, relativePath, "..")
	require.NoError(tb, os.MkdirAll(repoPath, perm.PrivateDir), "making repo parent dir")
	return &gitalypb.Repository{
		StorageName:   "default",
		RelativePath:  relativePath,
		GlRepository:  GlRepository,
		GlProjectPath: GlProjectPath,
	}
}

// NewObjectPoolName returns a random pool repository name in format
// '@pools/[0-9a-z]{2}/[0-9a-z]{2}/[0-9a-z]{64}.git'.
func NewObjectPoolName(tb testing.TB) string {
	return filepath.Join("@pools", newDiskHash(tb)+".git")
}

// NewRepositoryName returns a random repository hash
// in format '@hashed/[0-9a-f]{2}/[0-9a-f]{2}/[0-9a-f]{64}.git'.
func NewRepositoryName(tb testing.TB) string {
	return filepath.Join("@hashed", newDiskHash(tb)+".git")
}

// newDiskHash generates a random directory path following the Rails app's
// approach in the hashed storage module, formatted as '[0-9a-f]{2}/[0-9a-f]{2}/[0-9a-f]{64}'.
// https://gitlab.com/gitlab-org/gitlab/-/blob/f5c7d8eb1dd4eee5106123e04dec26d277ff6a83/app/models/storage/hashed.rb#L38-43
func newDiskHash(tb testing.TB) string {
	// rails app calculates a sha256 and uses its hex representation
	// as the directory path
	b, err := text.RandomHex(sha256.Size)
	require.NoError(tb, err)
	return filepath.Join(b[0:2], b[2:4], b)
}

// CreateRepositoryConfig allows for configuring how the repository is created.
type CreateRepositoryConfig struct {
	// ClientConn is the connection used to create the repository. If unset, the config is used to
	// dial the service.
	ClientConn *grpc.ClientConn
	// Storage determines the storage the repository is created in. If unset, the first storage
	// from the config is used.
	Storage config.Storage
	// RelativePath sets the relative path of the repository in the storage. If unset,
	// the relative path is set to a randomly generated hashed storage path
	RelativePath string
	// Seed determines which repository is used to seed the created repository. If unset, the repository
	// is just created. The value should be one of the test repositories in _build/testrepos.
	Seed string
	// SkipCreationViaService skips creation of the repository by calling the respective RPC call.
	// In general, this should not be skipped so that we end up in a state that is consistent
	// and expected by both Gitaly and Praefect. It may be required though when testing at a
	// level where there are no gRPC services available.
	SkipCreationViaService bool
	// ObjectFormat overrides the object format used by the repository.
	ObjectFormat string
}

func dialService(tb testing.TB, ctx context.Context, cfg config.Cfg) *grpc.ClientConn {
	tb.Helper()

	dialOptions := []grpc.DialOption{client.UnaryInterceptor(), client.StreamInterceptor()}
	if cfg.Auth.Token != "" {
		dialOptions = append(dialOptions, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(cfg.Auth.Token)))
	}

	var addr string
	switch {
	case cfg.SocketPath != "" && cfg.SocketPath != testcfg.UnconfiguredSocketPath:
		addr = cfg.SocketPath
	case cfg.ListenAddr != "":
		addr = cfg.ListenAddr
	case cfg.TLSListenAddr != "":
		addr = cfg.TLSListenAddr
	default:
		require.FailNow(tb, "cannot dial service without configured address")
	}

	conn, err := client.Dial(ctx, addr, client.WithGrpcOptions(dialOptions))
	require.NoError(tb, err)
	return conn
}

// CreateRepository creates a new repository and returns it and its absolute path.
func CreateRepository(tb testing.TB, ctx context.Context, cfg config.Cfg, configs ...CreateRepositoryConfig) (*gitalypb.Repository, string) {
	tb.Helper()

	require.Less(tb, len(configs), 2, "you must either pass no or exactly one option")

	opts := CreateRepositoryConfig{}
	if len(configs) == 1 {
		opts = configs[0]
	}

	if ObjectHashIsSHA256() || opts.ObjectFormat != "" {
		require.Empty(tb, opts.Seed, "seeded repository creation not supported with non-default object format")
	}

	storage := cfg.Storages[0]
	if (opts.Storage != config.Storage{}) {
		storage = opts.Storage
	}

	relativePath := NewRepositoryName(tb)
	if opts.RelativePath != "" {
		relativePath = opts.RelativePath
	}

	repository := &gitalypb.Repository{
		StorageName:   storage.Name,
		RelativePath:  relativePath,
		GlRepository:  GlRepository,
		GlProjectPath: GlProjectPath,
	}

	var repoPath string
	if !opts.SkipCreationViaService {
		conn := opts.ClientConn
		if conn == nil {
			conn = dialService(tb, ctx, cfg)
			tb.Cleanup(func() { testhelper.MustClose(tb, conn) })
		}
		client := gitalypb.NewRepositoryServiceClient(conn)

		if opts.Seed != "" {
			_, err := client.CreateRepositoryFromURL(ctx, &gitalypb.CreateRepositoryFromURLRequest{
				Repository: repository,
				Url:        testRepositoryPath(tb, opts.Seed),
				Mirror:     true,
			})
			require.NoError(tb, err)
		} else {
			objectFormat := opts.ObjectFormat
			if objectFormat == "" {
				objectFormat = DefaultObjectHash.Format
			}

			objectHash, err := git.ObjectHashByFormat(objectFormat)
			require.NoError(tb, err)

			_, err = client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
				Repository:   repository,
				ObjectFormat: objectHash.ProtoFormat,
			})
			require.NoError(tb, err)
		}

		tb.Cleanup(func() {
			// The ctx parameter would be canceled by now as the tests defer the cancellation.
			if _, err := client.RemoveRepository(context.TODO(), &gitalypb.RemoveRepositoryRequest{
				Repository: repository,
			}); err != nil {
				if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
					// The tests may delete the repository, so this is not a failure.
					return
				}

				tb.Logf("failed removing repository: %q", err)
			}
		})

		repoPath = filepath.Join(storage.Path, getReplicaPath(tb, ctx, conn, repository))
	} else {
		repoPath = filepath.Join(storage.Path, repository.RelativePath)

		if opts.Seed != "" {
			Exec(tb, cfg, "clone", "--no-hardlinks", "--dissociate", "--bare", testRepositoryPath(tb, opts.Seed), repoPath)
			Exec(tb, cfg, "-C", repoPath, "remote", "remove", "origin")
		} else {
			objectFormat := opts.ObjectFormat
			if objectFormat == "" {
				objectFormat = DefaultObjectHash.Format
			}

			Exec(tb, cfg, "init", "--bare", "--object-format="+objectFormat, repoPath)
		}

		tb.Cleanup(func() { require.NoError(tb, os.RemoveAll(repoPath)) })
	}

	// Return a cloned repository so the above clean up function still targets the correct repository
	// if the tests modify the returned repository.
	clonedRepo := proto.Clone(repository).(*gitalypb.Repository)

	return clonedRepo, repoPath
}

// GetReplicaPathConfig allows for configuring the GetReplicaPath call.
type GetReplicaPathConfig struct {
	// ClientConn is the connection used to create the repository. If unset, the config is used to
	// dial the service.
	ClientConn *grpc.ClientConn
}

// GetReplicaPath retrieves the repository's replica path if the test has been
// run with Praefect in front of it. This is necessary if the test creates a repository
// through Praefect and peeks into the filesystem afterwards. Conn should be pointing to
// Praefect.
func GetReplicaPath(tb testing.TB, ctx context.Context, cfg config.Cfg, repo storage.Repository, opts ...GetReplicaPathConfig) string {
	require.Less(tb, len(opts), 2, "you must either pass no or exactly one option")

	var opt GetReplicaPathConfig
	if len(opts) > 0 {
		opt = opts[0]
	}

	conn := opt.ClientConn
	if conn == nil {
		conn = dialService(tb, ctx, cfg)
		defer conn.Close()
	}

	return getReplicaPath(tb, ctx, conn, repo)
}

func getReplicaPath(tb testing.TB, ctx context.Context, conn *grpc.ClientConn, repo storage.Repository) string {
	metadata, err := gitalypb.NewPraefectInfoServiceClient(conn).GetRepositoryMetadata(
		ctx, &gitalypb.GetRepositoryMetadataRequest{
			Query: &gitalypb.GetRepositoryMetadataRequest_Path_{
				Path: &gitalypb.GetRepositoryMetadataRequest_Path{
					VirtualStorage: repo.GetStorageName(),
					RelativePath:   repo.GetRelativePath(),
				},
			},
		})
	if status, ok := status.FromError(err); ok && status.Code() == codes.Unimplemented && status.Message() == "unknown service gitaly.PraefectInfoService" {
		// The repository is stored at relative path if the test is running without Praefect in front.
		return repo.GetRelativePath()
	}
	require.NoError(tb, err)

	return metadata.ReplicaPath
}

// RewrittenRepository returns the repository as it would be received by a Gitaly after being rewritten by Praefect.
// This should be used when the repository is being accessed through the filesystem to ensure the access path is
// correct. If the test is not running with Praefect in front, it returns the an unaltered copy of repository.
func RewrittenRepository(tb testing.TB, ctx context.Context, cfg config.Cfg, repository *gitalypb.Repository) *gitalypb.Repository {
	// Don'tb modify the original repository.
	rewritten := proto.Clone(repository).(*gitalypb.Repository)
	rewritten.RelativePath = GetReplicaPath(tb, ctx, cfg, repository)
	return rewritten
}

// BundleRepo creates a bundle of a repository. `patterns` define the bundle contents as per
// `git-rev-list-args`. If there are no patterns then `--all` is assumed.
func BundleRepo(tb testing.TB, cfg config.Cfg, repoPath, bundlePath string, patterns ...string) []byte {
	if len(patterns) == 0 {
		patterns = []string{"--all"}
	}
	return Exec(tb, cfg, append([]string{"-C", repoPath, "bundle", "create", bundlePath}, patterns...)...)
}

// ChecksumRepo calculates the checksum of a repository.
func ChecksumRepo(tb testing.TB, cfg config.Cfg, repoPath string) *git.Checksum {
	var checksum git.Checksum
	lines := bytes.Split(Exec(tb, cfg, "-C", repoPath, "show-ref", "--head"), []byte("\n"))
	for _, line := range lines {
		checksum.AddBytes(line)
	}
	return &checksum
}

// testRepositoryPath returns the absolute path of local 'gitlab-org/gitlab-test.git' clone.
// It is cloned under the path by the test preparing step of make.
func testRepositoryPath(tb testing.TB, repo string) string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		require.Fail(tb, "could not get caller info")
	}

	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "_build", "testrepos", repo)
	if !isValidRepoPath(path) {
		makePath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..")
		makeTarget := "prepare-test-repos"
		tb.Logf("local clone of test repository %q not found in %q, running `make %v`", repo, path, makeTarget)
		testhelper.MustRunCommand(tb, nil, "make", "-C", makePath, makeTarget)
	}

	return path
}

// isValidRepoPath checks whether a valid git repository exists at the given path.
func isValidRepoPath(absolutePath string) bool {
	if _, err := os.Stat(filepath.Join(absolutePath, "objects")); err != nil {
		return false
	}

	return true
}

// AddWorktreeArgs returns git command arguments for adding a worktree at the
// specified repo
func AddWorktreeArgs(repoPath, worktreeName string) []string {
	return []string{"-C", repoPath, "worktree", "add", "--detach", worktreeName}
}

// AddWorktree creates a worktree in the repository path for tests
func AddWorktree(tb testing.TB, cfg config.Cfg, repoPath string, worktreeName string) {
	tb.Helper()
	Exec(tb, cfg, AddWorktreeArgs(repoPath, worktreeName)...)
}

// RepositoryPather is an interface for repositories that know about their path.
type RepositoryPather interface {
	Path(context.Context) (string, error)
}

// RepositoryPath returns the path of the given RepositoryPather. If any components are given, then
// the repository path and components are joined together.
func RepositoryPath(tb testing.TB, ctx context.Context, pather RepositoryPather, components ...string) string {
	tb.Helper()
	path, err := pather.Path(ctx)
	require.NoError(tb, err)
	return filepath.Join(append([]string{path}, components...)...)
}
