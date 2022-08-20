package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
	internallog "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/sentry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/duration"
)

const (
	// GitalyDataPrefix is the top-level directory we use to store system
	// (non-user) data. We need to be careful that this path does not clash
	// with any directory name that could be provided by a user. The '+'
	// character is not allowed in GitLab namespaces or repositories.
	GitalyDataPrefix = "+gitaly"
)

// DailyJob enables a daily task to be scheduled for specific storages
type DailyJob struct {
	Hour     uint              `toml:"start_hour"`
	Minute   uint              `toml:"start_minute"`
	Duration duration.Duration `toml:"duration"`
	Storages []string          `toml:"storages"`

	// Disabled will completely disable a daily job, even in cases where a
	// default schedule is implied
	Disabled bool `toml:"disabled"`
}

// Cfg is a container for all config derived from config.toml.
type Cfg struct {
	SocketPath             string            `toml:"socket_path" split_words:"true"`
	ListenAddr             string            `toml:"listen_addr" split_words:"true"`
	TLSListenAddr          string            `toml:"tls_listen_addr" split_words:"true"`
	PrometheusListenAddr   string            `toml:"prometheus_listen_addr" split_words:"true"`
	BinDir                 string            `toml:"bin_dir"`
	RuntimeDir             string            `toml:"runtime_dir"`
	Git                    Git               `toml:"git" envconfig:"git"`
	Storages               []Storage         `toml:"storage" envconfig:"storage"`
	Logging                Logging           `toml:"logging" envconfig:"logging"`
	Prometheus             prometheus.Config `toml:"prometheus"`
	Auth                   auth.Config       `toml:"auth"`
	TLS                    TLS               `toml:"tls"`
	Ruby                   Ruby              `toml:"gitaly-ruby"`
	Gitlab                 Gitlab            `toml:"gitlab"`
	GitlabShell            GitlabShell       `toml:"gitlab-shell"`
	Hooks                  Hooks             `toml:"hooks"`
	Concurrency            []Concurrency     `toml:"concurrency"`
	RateLimiting           []RateLimiting    `toml:"rate_limiting"`
	GracefulRestartTimeout duration.Duration `toml:"graceful_restart_timeout"`
	DailyMaintenance       DailyJob          `toml:"daily_maintenance"`
	Cgroups                cgroups.Config    `toml:"cgroups"`
	PackObjectsCache       StreamCacheConfig `toml:"pack_objects_cache"`
}

// TLS configuration
type TLS struct {
	CertPath string `toml:"certificate_path,omitempty" json:"cert_path"`
	KeyPath  string `toml:"key_path,omitempty" json:"key_path"`
}

// GitlabShell contains the settings required for executing `gitlab-shell`
type GitlabShell struct {
	Dir string `toml:"dir" json:"dir"`
}

// Gitlab contains settings required to connect to the Gitlab api
type Gitlab struct {
	URL             string       `toml:"url" json:"url"`
	RelativeURLRoot string       `toml:"relative_url_root" json:"relative_url_root"` // For UNIX sockets only
	HTTPSettings    HTTPSettings `toml:"http-settings" json:"http_settings"`
	SecretFile      string       `toml:"secret_file" json:"secret_file"`
}

// Hooks contains the settings required for hooks
type Hooks struct {
	CustomHooksDir string `toml:"custom_hooks_dir" json:"custom_hooks_dir"`
}

//nolint: stylecheck // This is unintentionally missing documentation.
type HTTPSettings struct {
	ReadTimeout int    `toml:"read_timeout" json:"read_timeout"`
	User        string `toml:"user" json:"user"`
	Password    string `toml:"password" json:"password"`
	CAFile      string `toml:"ca_file" json:"ca_file"`
	CAPath      string `toml:"ca_path" json:"ca_path"`
}

// Git contains the settings for the Git executable
type Git struct {
	UseBundledBinaries bool        `toml:"use_bundled_binaries"`
	BinPath            string      `toml:"bin_path"`
	CatfileCacheSize   int         `toml:"catfile_cache_size"`
	Config             []GitConfig `toml:"config"`
	IgnoreGitconfig    bool        `toml:"ignore_gitconfig"`
}

// GitConfig contains a key-value pair which is to be passed to git as configuration.
type GitConfig struct {
	Key   string `toml:"key"`
	Value string `toml:"value"`
}

// Storage contains a single storage-shard
type Storage struct {
	Name string
	Path string
}

// Sentry is a sentry.Config. We redefine this type to a different name so
// we can embed both structs into Logging
type Sentry sentry.Config

// Logging contains the logging configuration for Gitaly
type Logging struct {
	internallog.Config
	Sentry

	RubySentryDSN string `toml:"ruby_sentry_dsn"`
}

// Concurrency allows endpoints to be limited to a maximum concurrency per repo.
// Requests that come in after the maximum number of concurrent requests are in progress will wait
// in a queue that is bounded by MaxQueueSize.
type Concurrency struct {
	// RPC is the name of the RPC to set concurrency limits for
	RPC string `toml:"rpc"`
	// MaxPerRepo is the maximum number of concurrent calls for a given repository
	MaxPerRepo int `toml:"max_per_repo"`
	// MaxQueueSize is the maximum number of requests in the queue waiting to be picked up
	// after which subsequent requests will return with an error.
	MaxQueueSize int `toml:"max_queue_size"`
	// MaxQueueWait is the maximum time a request can remain in the concurrency queue
	// waiting to be picked up by Gitaly
	MaxQueueWait duration.Duration `toml:"max_queue_wait"`
}

// RateLimiting allows endpoints to be limited to a maximum request rate per
// second. The rate limiter uses a concept of a "token bucket". In order to serve a
// request, a token is retrieved from the token bucket. The size of the token
// bucket is configured through the Burst value, while the rate at which the
// token bucket is refilled per second is configured through the RequestsPerSecond
// value.
type RateLimiting struct {
	// RPC is the full name of the RPC including the service name
	RPC string `toml:"rpc"`
	// Interval sets the interval with which the token bucket will
	// be refilled to what is configured in Burst.
	Interval duration.Duration `toml:"interval"`
	// Burst sets the capacity of the token bucket (see above).
	Burst int `toml:"burst"`
}

// StreamCacheConfig contains settings for a streamcache instance.
type StreamCacheConfig struct {
	Enabled bool              `toml:"enabled"` // Default: false
	Dir     string            `toml:"dir"`     // Default: <FIRST STORAGE PATH>/+gitaly/PackObjectsCache
	MaxAge  duration.Duration `toml:"max_age"` // Default: 5m
}

// Load initializes the Config variable from file and the environment.
//  Environment variables take precedence over the file.
func Load(file io.Reader) (Cfg, error) {
	cfg := Cfg{
		Prometheus: prometheus.DefaultConfig(),
	}

	if err := toml.NewDecoder(file).Decode(&cfg); err != nil {
		return Cfg{}, fmt.Errorf("load toml: %v", err)
	}

	if err := cfg.setDefaults(); err != nil {
		return Cfg{}, err
	}

	for i := range cfg.Storages {
		cfg.Storages[i].Path = filepath.Clean(cfg.Storages[i].Path)
	}

	return cfg, nil
}

// Validate checks the current Config for sanity.
func (cfg *Cfg) Validate() error {
	for _, run := range []func() error{
		cfg.validateListeners,
		cfg.validateStorages,
		cfg.validateToken,
		cfg.validateGit,
		cfg.validateShell,
		cfg.ConfigureRuby,
		cfg.validateBinDir,
		cfg.validateRuntimeDir,
		cfg.validateMaintenance,
		cfg.validateCgroups,
		cfg.configurePackObjectsCache,
	} {
		if err := run(); err != nil {
			return err
		}
	}

	return nil
}

func (cfg *Cfg) setDefaults() error {
	if cfg.GracefulRestartTimeout.Duration() == 0 {
		cfg.GracefulRestartTimeout = duration.Duration(time.Minute)
	}

	if cfg.Gitlab.SecretFile == "" {
		cfg.Gitlab.SecretFile = filepath.Join(cfg.GitlabShell.Dir, ".gitlab_shell_secret")
	}

	if cfg.Hooks.CustomHooksDir == "" {
		cfg.Hooks.CustomHooksDir = filepath.Join(cfg.GitlabShell.Dir, "hooks")
	}

	if reflect.DeepEqual(cfg.DailyMaintenance, DailyJob{}) {
		cfg.DailyMaintenance = defaultMaintenanceWindow(cfg.Storages)
	}

	if cfg.Cgroups.Mountpoint == "" {
		cfg.Cgroups.Mountpoint = "/sys/fs/cgroup"
	}

	if cfg.Cgroups.HierarchyRoot == "" {
		cfg.Cgroups.HierarchyRoot = "gitaly"
	}

	cfg.Cgroups.FallbackToOldVersion()

	return nil
}

func (cfg *Cfg) validateListeners() error {
	if len(cfg.SocketPath) == 0 && len(cfg.ListenAddr) == 0 && len(cfg.TLSListenAddr) == 0 {
		return fmt.Errorf("at least one of socket_path, listen_addr or tls_listen_addr must be set")
	}
	return nil
}

func (cfg *Cfg) validateShell() error {
	if len(cfg.GitlabShell.Dir) == 0 {
		return fmt.Errorf("gitlab-shell.dir: is not set")
	}

	return validateIsDirectory(cfg.GitlabShell.Dir, "gitlab-shell.dir")
}

func validateIsDirectory(path, name string) error {
	s, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s: path doesn't exist: %q", name, path)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	if !s.IsDir() {
		return fmt.Errorf("%s: not a directory: %q", name, path)
	}

	log.WithField("dir", path).Debugf("%s set", name)

	return nil
}

// packedBinaries are the binaries that are packed in the main Gitaly binary. This should always match
// the actual list in <root>/packed_binaries.go so the binaries are correctly located.
//
// Resolving the names automatically from the packed binaries is not possible at the moment due to how
// the packed binaries themselves depend on this config package. If this config package inspected the
// packed binaries, there would be a cyclic dependency. Anything that the packed binaries import must
// not depend on <root>/packed_binaries.go.
var packedBinaries = map[string]struct{}{
	"gitaly-hooks":      {},
	"gitaly-ssh":        {},
	"gitaly-git2go":     {},
	"gitaly-lfs-smudge": {},
}

// BinaryPath returns the path to a given binary. BinaryPath does not do any validation, it simply joins the binaryName
// with the correct base directory depending on whether the binary is a packed binary or not.
func (cfg *Cfg) BinaryPath(binaryName string) string {
	baseDirectory := cfg.BinDir
	if _, ok := packedBinaries[binaryName]; ok {
		baseDirectory = cfg.RuntimeDir
	}

	return filepath.Join(baseDirectory, binaryName)
}

func (cfg *Cfg) validateStorages() error {
	if len(cfg.Storages) == 0 {
		return fmt.Errorf("no storage configurations found. Are you using the right format? https://gitlab.com/gitlab-org/gitaly/issues/397")
	}

	for i, storage := range cfg.Storages {
		if storage.Name == "" {
			return fmt.Errorf("empty storage name at declaration %d", i+1)
		}

		if storage.Path == "" {
			return fmt.Errorf("empty storage path for storage %q", storage.Name)
		}

		fs, err := os.Stat(storage.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("storage path %q for storage %q doesn't exist", storage.Path, storage.Name)
			}
			return fmt.Errorf("storage %q: %w", storage.Name, err)
		}

		if !fs.IsDir() {
			return fmt.Errorf("storage path %q for storage %q is not a dir", storage.Path, storage.Name)
		}

		for _, other := range cfg.Storages[:i] {
			if other.Name == storage.Name {
				return fmt.Errorf("storage %q is defined more than once", storage.Name)
			}

			if storage.Path == other.Path {
				// This is weird but we allow it for legacy gitlab.com reasons.
				continue
			}

			if strings.HasPrefix(storage.Path, other.Path) || strings.HasPrefix(other.Path, storage.Path) {
				// If storages have the same sub directory, that is allowed
				if filepath.Dir(storage.Path) == filepath.Dir(other.Path) {
					continue
				}
				return fmt.Errorf("storage paths may not nest: %q and %q", storage.Name, other.Name)
			}
		}
	}

	return nil
}

// StoragePath looks up the base path for storageName. The second boolean
// return value indicates if anything was found.
func (cfg *Cfg) StoragePath(storageName string) (string, bool) {
	storage, ok := cfg.Storage(storageName)
	return storage.Path, ok
}

// Storage looks up storageName.
func (cfg *Cfg) Storage(storageName string) (Storage, bool) {
	for _, storage := range cfg.Storages {
		if storage.Name == storageName {
			return storage, true
		}
	}
	return Storage{}, false
}

// InternalSocketDir returns the location of the internal socket directory.
func (cfg *Cfg) InternalSocketDir() string {
	return filepath.Join(cfg.RuntimeDir, "sock.d")
}

// InternalSocketPath is the path to the internal Gitaly socket.
func (cfg *Cfg) InternalSocketPath() string {
	return filepath.Join(cfg.InternalSocketDir(), "intern")
}

func (cfg *Cfg) validateBinDir() error {
	if len(cfg.BinDir) == 0 {
		return fmt.Errorf("bin_dir: is not set")
	}

	if err := validateIsDirectory(cfg.BinDir, "bin_dir"); err != nil {
		return err
	}

	var err error
	cfg.BinDir, err = filepath.Abs(cfg.BinDir)
	return err
}

func (cfg *Cfg) validateRuntimeDir() error {
	if cfg.RuntimeDir == "" {
		return nil
	}

	if err := validateIsDirectory(cfg.RuntimeDir, "runtime_dir"); err != nil {
		return err
	}

	var err error
	cfg.RuntimeDir, err = filepath.Abs(cfg.RuntimeDir)
	return err
}

// validateGitConfigKey does a best-effort check whether or not a given git config key is valid. It
// does not allow for assignments in keys, which is overly strict and does not allow some valid
// keys. It does avoid misinterpretation of keys though and should catch many cases of
// misconfiguration.
func validateGitConfigKey(key string) error {
	if key == "" {
		return errors.New("key cannot be empty")
	}
	if strings.Contains(key, "=") {
		return errors.New("key cannot contain assignment")
	}
	if !strings.Contains(key, ".") {
		return errors.New("key must contain at least one section")
	}
	if strings.HasPrefix(key, ".") || strings.HasSuffix(key, ".") {
		return errors.New("key must not start or end with a dot")
	}
	return nil
}

func (cfg *Cfg) validateGit() error {
	for _, configPair := range cfg.Git.Config {
		if err := validateGitConfigKey(configPair.Key); err != nil {
			return fmt.Errorf("invalid configuration key %q: %w", configPair.Key, err)
		}
		if configPair.Value == "" {
			return fmt.Errorf("invalid configuration value: %q", configPair.Value)
		}
	}

	return nil
}

func (cfg *Cfg) validateToken() error {
	if !cfg.Auth.Transitioning || len(cfg.Auth.Token) == 0 {
		return nil
	}

	log.Warn("Authentication is enabled but not enforced because transitioning=true. Gitaly will accept unauthenticated requests.")
	return nil
}

// defaultMaintenanceWindow specifies a 10 minute job that runs daily at +1200
// GMT time
func defaultMaintenanceWindow(storages []Storage) DailyJob {
	storageNames := make([]string, len(storages))
	for i, s := range storages {
		storageNames[i] = s.Name
	}

	return DailyJob{
		Hour:     12,
		Minute:   0,
		Duration: duration.Duration(10 * time.Minute),
		Storages: storageNames,
	}
}

func (cfg *Cfg) validateMaintenance() error {
	dm := cfg.DailyMaintenance

	sNames := map[string]struct{}{}
	for _, s := range cfg.Storages {
		sNames[s.Name] = struct{}{}
	}
	for _, sName := range dm.Storages {
		if _, ok := sNames[sName]; !ok {
			return fmt.Errorf("daily maintenance specified storage %q does not exist in configuration", sName)
		}
	}

	if dm.Hour > 23 {
		return fmt.Errorf("daily maintenance specified hour '%d' outside range (0-23)", dm.Hour)
	}
	if dm.Minute > 59 {
		return fmt.Errorf("daily maintenance specified minute '%d' outside range (0-59)", dm.Minute)
	}
	if dm.Duration.Duration() > 24*time.Hour {
		return fmt.Errorf("daily maintenance specified duration %s must be less than 24 hours", dm.Duration.Duration())
	}

	return nil
}

func (cfg *Cfg) validateCgroups() error {
	cg := cfg.Cgroups

	if cg.MemoryBytes > 0 && (cg.Repositories.MemoryBytes > cg.MemoryBytes) {
		return errors.New("cgroups.repositories: memory limit cannot exceed parent")
	}

	if cg.MemoryBytes > 0 && (cg.Repositories.CPUShares > cg.CPUShares) {
		return errors.New("cgroups.repositories: cpu shares cannot exceed parent")
	}

	return nil
}

var (
	errPackObjectsCacheNegativeMaxAge = errors.New("pack_objects_cache.max_age cannot be negative")
	errPackObjectsCacheNoStorages     = errors.New("pack_objects_cache: cannot pick default cache directory: no storages")
	errPackObjectsCacheRelativePath   = errors.New("pack_objects_cache: storage directory must be absolute path")
)

func (cfg *Cfg) configurePackObjectsCache() error {
	poc := &cfg.PackObjectsCache
	if !poc.Enabled {
		return nil
	}

	if poc.MaxAge < 0 {
		return errPackObjectsCacheNegativeMaxAge
	}

	if poc.MaxAge == 0 {
		poc.MaxAge = duration.Duration(5 * time.Minute)
	}

	if poc.Dir == "" {
		if len(cfg.Storages) == 0 {
			return errPackObjectsCacheNoStorages
		}

		poc.Dir = filepath.Join(cfg.Storages[0].Path, GitalyDataPrefix, "PackObjectsCache")
	}

	if !filepath.IsAbs(poc.Dir) {
		return errPackObjectsCacheRelativePath
	}

	return nil
}

// PruneRuntimeDirectories removes leftover runtime directories that belonged to processes that
// no longer exist. The removals are logged prior to being executed. Unexpected directory entries
// are logged but not removed
func PruneRuntimeDirectories(log log.FieldLogger, runtimeDir string) error {
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return fmt.Errorf("list runtime directory: %w", err)
	}

	for _, entry := range entries {
		if err := func() error {
			log := log.WithField("path", filepath.Join(runtimeDir, entry.Name()))
			if !entry.IsDir() {
				// There should be no files, only the runtime directories.
				log.Error("runtime directory contains an unexpected file")
				return nil
			}

			components := strings.Split(entry.Name(), "-")
			if len(components) != 2 || components[0] != "gitaly" {
				// This directory does not match the runtime directory naming format
				// of `gitaly-<process id>.
				log.Error("runtime directory contains an unexpected directory")
				return nil
			}

			processID, err := strconv.ParseInt(components[1], 10, 64)
			if err != nil {
				// This is not a runtime directory as the section after the hyphen is not a process id.
				log.Error("runtime directory contains an unexpected directory")
				return nil
			}

			process, err := os.FindProcess(int(processID))
			if err != nil {
				return fmt.Errorf("find process: %w", err)
			}
			defer func() {
				if err := process.Release(); err != nil {
					log.WithError(err).Error("failed releasing process")
				}
			}()

			if err := process.Signal(syscall.Signal(0)); err != nil {
				// Either the process does not exist, or the pid has been re-used by for a
				// process owned by another user and is not a Gitaly process.
				if !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.EPERM) {
					return fmt.Errorf("signal: %w", err)
				}

				log.Info("removing leftover runtime directory")

				if err := os.RemoveAll(filepath.Join(runtimeDir, entry.Name())); err != nil {
					return fmt.Errorf("remove leftover runtime directory: %w", err)
				}
			}

			return nil
		}(); err != nil {
			return err
		}
	}

	return nil
}

// SetupRuntimeDirectory creates a new runtime directory. Runtime directory contains internal
// runtime data generated by Gitaly such as the internal sockets. If cfg.RuntimeDir is set,
// it's used as the parent directory for the runtime directory. Runtime directory owner process
// can be identified by the suffix process ID suffixed in the directory name. If a directory already
// exists for this process' ID, it's removed and recreated. If cfg.RuntimeDir is not set, a temporary
// directory is used instead. A directory is created for the internal socket as well since it is
// expected to be present in the runtime directory. SetupRuntimeDirectory returns the absolute path
// to the created runtime directory.
func SetupRuntimeDirectory(cfg Cfg, processID int) (string, error) {
	var runtimeDir string
	if cfg.RuntimeDir == "" {
		// If there is no parent directory provided, we just use a temporary directory
		// as the runtime directory. This may not always be an ideal choice given that
		// it's typically created at `/tmp`, which may get periodically pruned if `noatime`
		// is set.
		var err error
		runtimeDir, err = os.MkdirTemp("", "gitaly-")
		if err != nil {
			return "", fmt.Errorf("creating temporary runtime directory: %w", err)
		}
	} else {
		// Otherwise, we use the configured runtime directory. Note that we don't use the
		// runtime directory directly, but instead create a subdirectory within it which is
		// based on the process's PID. While we could use `MkdirTemp()` instead and don't
		// bother with preexisting directories, the benefit of using the PID here is that we
		// can determine whether the directory may still be in use by checking whether the
		// PID exists. Furthermore, it allows easier debugging in case one wants to inspect
		// the runtime directory of a running Gitaly node.

		runtimeDir = filepath.Join(cfg.RuntimeDir, fmt.Sprintf("gitaly-%d", processID))

		if _, err := os.Stat(runtimeDir); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("statting runtime directory: %w", err)
		} else if err != nil {
			// If the directory exists already then it must be from an old invocation of
			// Gitaly. Because we use the PID as path component we know that the old
			// instance cannot exist anymore though, so it's safe to remove this
			// directory now.
			if err := os.RemoveAll(runtimeDir); err != nil {
				return "", fmt.Errorf("removing old runtime directory: %w", err)
			}
		}

		if err := os.Mkdir(runtimeDir, 0o700); err != nil {
			return "", fmt.Errorf("creating runtime directory: %w", err)
		}
	}

	// Set the runtime dir in the config as the internal socket helpers
	// rely on it.
	cfg.RuntimeDir = runtimeDir

	// The socket path must be short-ish because listen(2) fails on long
	// socket paths. We hope/expect that os.MkdirTemp creates a directory
	// that is not too deep. We need a directory, not a tempfile, because we
	// will later want to set its permissions to 0700
	if err := os.Mkdir(cfg.InternalSocketDir(), 0o700); err != nil {
		return "", fmt.Errorf("create internal socket directory: %w", err)
	}

	if err := trySocketCreation(cfg.InternalSocketDir()); err != nil {
		return "", fmt.Errorf("failed creating internal test socket: %w", err)
	}

	return runtimeDir, nil
}

func trySocketCreation(dir string) error {
	// To validate the socket can actually be created, we open and close a socket.
	// Any error will be assumed persistent for when the gitaly-ruby sockets are created
	// and thus fatal at boot time.
	//
	// There are two kinds of internal sockets we create: the internal server socket
	// called "intern", and then the Ruby worker sockets called "ruby.$N", with "$N"
	// being the number of the Ruby worker. Given that we typically wouldn't spawn
	// hundreds of Ruby workers, the maximum internal socket path name would thus be 7
	// characters long.
	socketPath := filepath.Join(dir, "tsocket")
	defer func() { _ = os.Remove(socketPath) }()

	// Attempt to create an actual socket and not just a file to catch socket path length problems
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) && errno == syscall.EINVAL {
			return fmt.Errorf("%w: your socket path is likely too long, please change Gitaly's runtime directory", errno)
		}

		return fmt.Errorf("socket could not be created in %s: %w", dir, err)
	}

	return l.Close()
}
