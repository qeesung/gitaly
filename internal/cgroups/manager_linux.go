//go:build linux

package cgroups

import (
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	cgrps "github.com/containerd/cgroups/v3"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/prometheus/client_golang/prometheus"
	cgroupscfg "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

// cfs_period_us hardcoded to be 100ms.
const cfsPeriodUs uint64 = 100000

type cgroupHandler interface {
	setupParent(parentResources *specs.LinuxResources) error
	createCgroup(repoResources *specs.LinuxResources, cgroupPath string) error
	addToCgroup(pid int, cgroupPath string) error
	collect(repoPath string, ch chan<- prometheus.Metric)
	currentProcessCgroup() string
	repoPath(groupID int) string
	stats() (Stats, error)
	supportsCloneIntoCgroup() bool
}

type cgroupLock struct {
	created       atomic.Bool
	creationError error
	once          sync.Once
}

func (l *cgroupLock) isCreated() bool {
	return l.created.Load()
}

type cgroupStatus struct {
	m map[string]*cgroupLock
}

func newCgroupStatus(cfg cgroupscfg.Config, repoPath func(int) string) *cgroupStatus {
	status := &cgroupStatus{
		m: make(map[string]*cgroupLock, cfg.Repositories.Count),
	}

	// Pre-fill the map with all possible values.
	for i := 0; i < int(cfg.Repositories.Count); i++ {
		cgroupPath := repoPath(i)
		cgLock := &cgroupLock{}
		status.m[cgroupPath] = cgLock
	}

	return status
}

func (s *cgroupStatus) getLock(cgroupPath string) *cgroupLock {
	// We initialized all locks during construction.
	cgLock := s.m[cgroupPath]

	return cgLock
}

// randomizer is the interface of the Go random number generator.
type randomizer interface {
	// Intn returns a random integer in the range [0,n).
	Intn(n int) int
}

// CGroupManager is a manager class that implements specific methods related to cgroups
type CGroupManager struct {
	cfg     cgroupscfg.Config
	pid     int
	enabled bool
	repoRes *specs.LinuxResources
	status  *cgroupStatus
	handler cgroupHandler
	rand    randomizer
}

func newCgroupManager(cfg cgroupscfg.Config, logger log.Logger, pid int) *CGroupManager {
	return newCgroupManagerWithMode(cfg, logger, pid, cgrps.Mode())
}

func newCgroupManagerWithMode(cfg cgroupscfg.Config, logger log.Logger, pid int, mode cgrps.CGMode) *CGroupManager {
	var handler cgroupHandler
	switch mode {
	case cgrps.Legacy, cgrps.Hybrid:
		handler = newV1Handler(cfg, logger, pid)
	case cgrps.Unified:
		handler = newV2Handler(cfg, logger, pid)
		logger.Warn("Gitaly now includes experimental support for CgroupV2. Please proceed with caution and use this experimental feature at your own risk")
	default:
		logger.Warn("Gitaly has encountered an issue while trying to detect the version of the system's cgroup. As a result, all subsequent commands will be executed without cgroup support. Please check the system's cgroup configuration and try again")
		return nil
	}

	return &CGroupManager{
		cfg:     cfg,
		pid:     pid,
		handler: handler,
		status:  newCgroupStatus(cfg, handler.repoPath),
		rand:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Setup parent cgroups and repository sub cgroups
func (cgm *CGroupManager) Setup() error {
	if err := cgm.handler.setupParent(cgm.configParentResources()); err != nil {
		return err
	}
	repoResources, err := configRepositoryResources(cgm.cfg)
	if errors.As(err, &CgroupValidationError{}) {
		fmt.Printf("Validation failed: %s\n", err)
	}
	cgm.repoRes = repoResources
	cgm.enabled = true

	return nil
}

// Ready returns true if the Cgroup manager is configured and ready to use.
func (cgm *CGroupManager) Ready() bool {
	return cgm.enabled
}

// AddCommand adds a Cmd to a cgroup
func (cgm *CGroupManager) AddCommand(cmd *exec.Cmd, opts ...AddCommandOption) (string, error) {
	if cmd.Process == nil {
		return "", errors.New("cannot add command that has not yet been started")
	}

	cgroupPath := cgm.cgroupPathForCommand(cmd, opts)

	if err := cgm.maybeCreateCgroup(cgroupPath); err != nil {
		return "", fmt.Errorf("setup cgroup: %w", err)
	}

	return cgroupPath, cgm.handler.addToCgroup(cmd.Process.Pid, cgroupPath)
}

// SupportsCloneIntoCgroup returns whether this Manager supports the CloneIntoCgroup method.
// CloneIntoCgroup requires CLONE_INTO_CGROUP which is only supported with cgroup version 2
// with Linux 5.7 or newer.
func (cgm *CGroupManager) SupportsCloneIntoCgroup() bool {
	return cgm.handler.supportsCloneIntoCgroup()
}

// maybeCreateCgroup creates a cgroup if it yet hasn't been created.
func (cgm *CGroupManager) maybeCreateCgroup(cgroupPath string) error {
	lock := cgm.status.getLock(cgroupPath)

	lock.once.Do(func() {
		if err := cgm.handler.createCgroup(cgm.repoRes, cgroupPath); err != nil {
			lock.creationError = err
			return
		}

		lock.created.Store(true)
	})

	return lock.creationError
}

// CloneIntoCgroup configures the cgroup parameters UseCgroupFD and CgroupFD in SysProcAttr
// to start the command directly in the correct cgroup. On success, the function returns an io.Closer
// that must be closed after the command has been started to close the cgroup's file descriptor.
func (cgm *CGroupManager) CloneIntoCgroup(cmd *exec.Cmd, opts ...AddCommandOption) (string, io.Closer, error) {
	cgroupPath := cgm.cgroupPathForCommand(cmd, opts)

	if err := cgm.maybeCreateCgroup(cgroupPath); err != nil {
		return "", nil, fmt.Errorf("setup cgroup: %w", err)
	}

	cgroupDirPath := filepath.Join(cgm.cfg.Mountpoint, cgroupPath)
	dir, err := os.Open(cgroupDirPath)
	if err != nil {
		return "", nil, fmt.Errorf("open file: %w", err)
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(dir.Fd())

	return cgroupDirPath, dir, nil
}

// cgroupPathForCommand returns the path of the cgroup a given command should go in.
func (cgm *CGroupManager) cgroupPathForCommand(cmd *exec.Cmd, opts []AddCommandOption) string {
	var cfg addCommandCfg
	for _, opt := range opts {
		opt(&cfg)
	}

	key := cfg.cgroupKey
	if key == "" {
		key = strings.Join(cmd.Args, "/")
	}
	groupID := cgm.calcGroupID(cgm.rand, key, cgm.cfg.Repositories.Count, cgm.cfg.Repositories.MaxCgroupsPerRepo)
	return cgm.handler.repoPath(int(groupID))
}

func (cgm *CGroupManager) calcGroupID(rand randomizer, key string, count uint, allocationCount uint) uint {
	checksum := crc32.ChecksumIEEE([]byte(key))

	// Pick a starting point
	groupID := uint(checksum) % count
	if allocationCount <= 1 {
		return groupID
	}

	// Shift random distance [0, allocation) from the starting point. Wrap-around if needed.
	return (groupID + uint(rand.Intn(int(allocationCount)))) % count
}

// Describe is used to generate description information for each CGroupManager prometheus metric
func (cgm *CGroupManager) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(cgm, ch)
}

// Collect is used to collect the current values of all CGroupManager prometheus metrics
func (cgm *CGroupManager) Collect(ch chan<- prometheus.Metric) {
	if !cgm.cfg.MetricsEnabled {
		return
	}

	for i := 0; i < int(cgm.cfg.Repositories.Count); i++ {
		repoPath := cgm.handler.repoPath(i)

		cgLock := cgm.status.getLock(repoPath)
		if !cgLock.isCreated() {
			continue
		}

		cgm.handler.collect(repoPath, ch)
	}
}

// Stats returns cgroup accounting statistics collected by reading
// cgroupfs files.
func (cgm *CGroupManager) Stats() (Stats, error) {
	return cgm.handler.stats()
}

func (cgm *CGroupManager) currentProcessCgroup() string {
	return cgm.handler.currentProcessCgroup()
}

func (cgm *CGroupManager) configParentResources() *specs.LinuxResources {
	cfsPeriodUs := cfsPeriodUs
	var parentResources specs.LinuxResources
	// Leave them `nil` so it takes kernel default unless cfg value above `0`.
	parentResources.CPU = &specs.LinuxCPU{}

	if cgm.cfg.CPUShares > 0 {
		parentResources.CPU.Shares = &cgm.cfg.CPUShares
	}

	if cgm.cfg.CPUQuotaUs > 0 {
		parentResources.CPU.Quota = &cgm.cfg.CPUQuotaUs
		parentResources.CPU.Period = &cfsPeriodUs
	}

	if cgm.cfg.MemoryBytes > 0 {
		parentResources.Memory = &specs.LinuxMemory{Limit: &cgm.cfg.MemoryBytes}
	}
	return &parentResources
}

func configRepositoryResources(cfg cgroupscfg.Config) (*specs.LinuxResources, error) {
	cfsPeriodUs := cfsPeriodUs
	var reposResources specs.LinuxResources
	// Leave them `nil` so it takes kernel default unless cfg value above `0`.
	reposResources.CPU = &specs.LinuxCPU{}

	if cfg.Repositories.CPUShares <= 0 || cfg.Repositories.CPUShares > cfg.CPUShares {
		return nil, CgroupValidationError{Field: "CPUShares",
			Msg: "Repository CPUShares must be less than or equal to parent's CPUShares"}
	}

	reposResources.CPU.Shares = &cfg.Repositories.CPUShares

	if cfg.Repositories.CPUQuotaUs <= 0 || cfg.Repositories.CPUQuotaUs > cfg.CPUQuotaUs {
		return nil, CgroupValidationError{Field: "name",
			Msg: "Repository CPUQuotaUs must be less than or equal to parent's CPUQuotaUs"}
	}

	reposResources.CPU.Quota = &cfg.Repositories.CPUQuotaUs
	reposResources.CPU.Period = &cfsPeriodUs

	if cfg.Repositories.MemoryBytes <= 0 || cfg.Repositories.MemoryBytes > cfg.MemoryBytes {
		return nil, CgroupValidationError{Field: "name",
			Msg: "Repository MemoryBytes must be less than or equal to parent's MemoryBytes"}
	}

	reposResources.Memory = &specs.LinuxMemory{Limit: &cfg.Repositories.MemoryBytes}
	return &reposResources, nil
}

func pruneOldCgroups(cfg cgroupscfg.Config, logger log.Logger) {
	pruneOldCgroupsWithMode(cfg, logger, cgrps.Mode())
}

func pruneOldCgroupsWithMode(cfg cgroupscfg.Config, logger log.Logger, mode cgrps.CGMode) {
	if cfg.HierarchyRoot == "" {
		return
	}

	switch mode {
	case cgrps.Legacy, cgrps.Hybrid:
		pruneOldCgroupsV1(cfg, logger)
	case cgrps.Unified:
		pruneOldCgroupsV2(cfg, logger)
	default:
	}
}
