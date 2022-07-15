package gittest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
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

	// SeedGitLabTest is the path of the gitlab-test.git repository in _build/testrepos
	SeedGitLabTest = "gitlab-test.git"

	// SeedGitLabTestMirror is the path of the gitlab-test-mirror.git repository in _build/testrepos
	SeedGitLabTestMirror = "gitlab-test-mirror.git"
)

// InitRepoDir creates a temporary directory for a repo, without initializing it
func InitRepoDir(t testing.TB, storagePath, relativePath string) *gitalypb.Repository {
	repoPath := filepath.Join(storagePath, relativePath, "..")
	require.NoError(t, os.MkdirAll(repoPath, 0o755), "making repo parent dir")
	return &gitalypb.Repository{
		StorageName:   "default",
		RelativePath:  relativePath,
		GlRepository:  GlRepository,
		GlProjectPath: GlProjectPath,
	}
}

// NewObjectPoolName returns a random pool repository name in format
// '@pools/[0-9a-z]{2}/[0-9a-z]{2}/[0-9a-z]{64}.git'.
func NewObjectPoolName(t testing.TB) string {
	return filepath.Join("@pools", newDiskHash(t)+".git")
}

// NewRepositoryName returns a random repository hash
// in format '@hashed/[0-9a-f]{2}/[0-9a-f]{2}/[0-9a-f]{64}(.git)?'.
func NewRepositoryName(t testing.TB, bare bool) string {
	suffix := ""
	if bare {
		suffix = ".git"
	}

	return filepath.Join("@hashed", newDiskHash(t)+suffix)
}

// newDiskHash generates a random directory path following the Rails app's
// approach in the hashed storage module, formatted as '[0-9a-f]{2}/[0-9a-f]{2}/[0-9a-f]{64}'.
// https://gitlab.com/gitlab-org/gitlab/-/blob/f5c7d8eb1dd4eee5106123e04dec26d277ff6a83/app/models/storage/hashed.rb#L38-43
func newDiskHash(t testing.TB) string {
	// rails app calculates a sha256 and uses its hex representation
	// as the directory path
	b, err := text.RandomHex(sha256.Size)
	require.NoError(t, err)
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
}

func dialService(ctx context.Context, t testing.TB, cfg config.Cfg) *grpc.ClientConn {
	dialOptions := []grpc.DialOption{}
	if cfg.Auth.Token != "" {
		dialOptions = append(dialOptions, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(cfg.Auth.Token)))
	}

	conn, err := client.DialContext(ctx, cfg.SocketPath, dialOptions)
	require.NoError(t, err)
	return conn
}

// CreateRepository creates a new repository and returns it and its absolute path.
func CreateRepository(ctx context.Context, t testing.TB, cfg config.Cfg, configs ...CreateRepositoryConfig) (*gitalypb.Repository, string) {
	t.Helper()

	require.Less(t, len(configs), 2, "you must either pass no or exactly one option")

	opts := CreateRepositoryConfig{}
	if len(configs) == 1 {
		opts = configs[0]
	}

	conn := opts.ClientConn
	if conn == nil {
		conn = dialService(ctx, t, cfg)
		t.Cleanup(func() { conn.Close() })
	}

	client := gitalypb.NewRepositoryServiceClient(conn)

	storage := cfg.Storages[0]
	if (opts.Storage != config.Storage{}) {
		storage = opts.Storage
	}

	relativePath := NewRepositoryName(t, true)
	if opts.RelativePath != "" {
		relativePath = opts.RelativePath
	}

	repository := &gitalypb.Repository{
		StorageName:   storage.Name,
		RelativePath:  relativePath,
		GlRepository:  GlRepository,
		GlProjectPath: GlProjectPath,
	}

	if opts.Seed != "" {
		_, err := client.CreateRepositoryFromURL(ctx, &gitalypb.CreateRepositoryFromURLRequest{
			Repository: repository,
			Url:        testRepositoryPath(t, opts.Seed),
			Mirror:     true,
		})
		require.NoError(t, err)
	} else {
		_, err := client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
			Repository: repository,
		})
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		// The ctx parameter would be canceled by now as the tests defer the cancellation.
		_, err := client.RemoveRepository(context.TODO(), &gitalypb.RemoveRepositoryRequest{
			Repository: repository,
		})

		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			// The tests may delete the repository, so this is not a failure.
			return
		}

		require.NoError(t, err)
	})

	// Return a cloned repository so the above clean up function still targets the correct repository
	// if the tests modify the returned repository.
	clonedRepo := proto.Clone(repository).(*gitalypb.Repository)

	return clonedRepo, filepath.Join(storage.Path, getReplicaPath(ctx, t, conn, repository))
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
func GetReplicaPath(ctx context.Context, t testing.TB, cfg config.Cfg, repo repository.GitRepo, opts ...GetReplicaPathConfig) string {
	require.Less(t, len(opts), 2, "you must either pass no or exactly one option")

	var opt GetReplicaPathConfig
	if len(opts) > 0 {
		opt = opts[0]
	}

	conn := opt.ClientConn
	if conn == nil {
		conn = dialService(ctx, t, cfg)
		defer conn.Close()
	}

	return getReplicaPath(ctx, t, conn, repo)
}

func getReplicaPath(ctx context.Context, t testing.TB, conn *grpc.ClientConn, repo repository.GitRepo) string {
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
	require.NoError(t, err)

	return metadata.ReplicaPath
}

// RewrittenRepository returns the repository as it would be received by a Gitaly after being rewritten by Praefect.
// This should be used when the repository is being accessed through the filesystem to ensure the access path is
// correct. If the test is not running with Praefect in front, it returns the an unaltered copy of repository.
func RewrittenRepository(ctx context.Context, t testing.TB, cfg config.Cfg, repository *gitalypb.Repository) *gitalypb.Repository {
	// Don't modify the original repository.
	rewritten := proto.Clone(repository).(*gitalypb.Repository)
	rewritten.RelativePath = GetReplicaPath(ctx, t, cfg, repository)
	return rewritten
}

// InitRepoOpts contains options for InitRepo.
type InitRepoOpts struct {
	// WithRelativePath determines the relative path of this repository.
	WithRelativePath string
}

// InitRepo creates a new empty repository in the given storage. You can either pass no or exactly
// one InitRepoOpts.
func InitRepo(t testing.TB, cfg config.Cfg, storage config.Storage, opts ...InitRepoOpts) (*gitalypb.Repository, string) {
	require.Less(t, len(opts), 2, "you must either pass no or exactly one option")

	opt := InitRepoOpts{}
	if len(opts) == 1 {
		opt = opts[0]
	}

	relativePath := opt.WithRelativePath
	if relativePath == "" {
		relativePath = NewRepositoryName(t, true)
	}
	repoPath := filepath.Join(storage.Path, relativePath)

	args := []string{"init", "--bare"}

	Exec(t, cfg, append(args, repoPath)...)

	repo := InitRepoDir(t, storage.Path, relativePath)
	repo.StorageName = storage.Name

	t.Cleanup(func() { require.NoError(t, os.RemoveAll(repoPath)) })

	return repo, repoPath
}

// CloneRepoOpts is an option for CloneRepo.
type CloneRepoOpts struct {
	// RelativePath determines the relative path of newly created Git repository. If unset, the
	// relative path is computed via NewRepositoryName.
	RelativePath string
	// SourceRepo determines the name of the source repository which shall be cloned. The source
	// repository is assumed to be relative to "_build/testrepos". If unset, defaults to
	// "gitlab-test.git".
	SourceRepo string
}

// CloneRepo clones a new copy of test repository under a subdirectory in the storage root. You can
// either pass no or exactly one CloneRepoOpts.
func CloneRepo(t testing.TB, cfg config.Cfg, storage config.Storage, opts ...CloneRepoOpts) (*gitalypb.Repository, string) {
	require.Less(t, len(opts), 2, "you must either pass no or exactly one option")

	opt := CloneRepoOpts{}
	if len(opts) == 1 {
		opt = opts[0]
	}

	relativePath := opt.RelativePath
	if relativePath == "" {
		relativePath = NewRepositoryName(t, true)
	}

	sourceRepo := opt.SourceRepo
	if sourceRepo == "" {
		sourceRepo = "gitlab-test.git"
	}

	repo := InitRepoDir(t, storage.Path, relativePath)
	repo.StorageName = storage.Name

	args := []string{"clone", "--no-hardlinks", "--dissociate", "--bare"}

	absolutePath := filepath.Join(storage.Path, relativePath)
	Exec(t, cfg, append(args, testRepositoryPath(t, sourceRepo), absolutePath)...)
	Exec(t, cfg, "-C", absolutePath, "remote", "remove", "origin")

	t.Cleanup(func() { require.NoError(t, os.RemoveAll(absolutePath)) })

	return repo, absolutePath
}

// BundleRepo creates a bundle of a repository. `patterns` define the bundle contents as per
// `git-rev-list-args`. If there are no patterns then `--all` is assumed.
func BundleRepo(t testing.TB, cfg config.Cfg, repoPath, bundlePath string, patterns ...string) {
	if len(patterns) == 0 {
		patterns = []string{"--all"}
	}
	Exec(t, cfg, append([]string{"-C", repoPath, "bundle", "create", bundlePath}, patterns...)...)
}

// ChecksumRepo calculates the checksum of a repository.
func ChecksumRepo(t testing.TB, cfg config.Cfg, repoPath string) *git.Checksum {
	var checksum git.Checksum
	lines := bytes.Split(Exec(t, cfg, "-C", repoPath, "show-ref", "--head"), []byte("\n"))
	for _, line := range lines {
		checksum.AddBytes(line)
	}
	return &checksum
}

// testRepositoryPath returns the absolute path of local 'gitlab-org/gitlab-test.git' clone.
// It is cloned under the path by the test preparing step of make.
func testRepositoryPath(t testing.TB, repo string) string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		require.Fail(t, "could not get caller info")
	}

	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "_build", "testrepos", repo)
	if !isValidRepoPath(path) {
		makePath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..")
		makeTarget := "prepare-test-repos"
		log.Printf("local clone of test repository %q not found in %q, running `make %v`", repo, path, makeTarget)
		testhelper.MustRunCommand(t, nil, "make", "-C", makePath, makeTarget)
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
func AddWorktree(t testing.TB, cfg config.Cfg, repoPath string, worktreeName string) {
	Exec(t, cfg, AddWorktreeArgs(repoPath, worktreeName)...)
}

// FixGitLabTestRepoForCommitGraphs fixes the "gitlab-test.git" repository so that it can be used in
// the context of commit-graphs. The test repository contains the commit ba3343b (Weird commit date,
// 292278994-08-17). As you can already see, this commit has a commit year of 292278994, which is
// not exactly a realistic commit date to have in normal repositories. Unfortunately, this commit
// date causes commit-graphs to become corrupt with the following error that's likely caused by
// an overflow:
//
//     commit date for commit ba3343bc4fa403a8dfbfcab7fc1a8c29ee34bd69 in commit-graph is 15668040695 != 9223372036854775
//
// This is not a new error, but something that has existed for quite a while already in Git. And
// while the bug can also be easily hit in Gitaly because we do write commit-graphs in pool
// repositories, until now we haven't because we never exercised this.
//
// Unfortunately, we're between a rock and a hard place: this error will be hit when running
// git-fsck(1) to find dangling objects, which we do to rescue objects. git-fsck(1) will by default
// verify the commit-graphs to be consistent even  with `--connectivity-only`, which causes the
// error. But while we could in theory just disable the usage of commit-graphs by passing
// `core.commitGraph=0`, the end result would be that the connectivity check itself may become a lot
// slower.
//
// So for now we just bail on this whole topic: it's not a new bug and we can't do much about it
// given it could regress performance. The pool members would be broken in the same way, even though
// less visibly so because we don't git-fsck(1) in "normal" RPCs. But to make our tests work we
// delete the reference for this specific commit so that it doesn't cause our tests to break.
//
// You can easily test whether this bug still exists via the following commands:
//
//     $ git clone _build/testrepos/gitlab-test.git
//     $ git -C gitlab-test commit-graph write
//     $ git -C gitlab-test commit-graph verify
func FixGitLabTestRepoForCommitGraphs(tb testing.TB, cfg config.Cfg, repoPath string) {
	Exec(tb, cfg, "-C", repoPath, "update-ref", "-d", "refs/heads/spooky-stuff", "ba3343bc4fa403a8dfbfcab7fc1a8c29ee34bd69")
}
