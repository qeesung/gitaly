package cgroups

import (
	"errors"
	"fmt"
	"hash/crc32"
	"strings"

	"github.com/containerd/cgroups"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	cgroupscfg "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v15/internal/log"
)

// CGroupV1Manager is the manager for cgroups v1
type CGroupV1Manager struct {
	cfg                                  cgroupscfg.Config
	hierarchy                            func() ([]cgroups.Subsystem, error)
	memoryReclaimAttemptsTotal, cpuUsage *prometheus.GaugeVec
	procs                                *prometheus.GaugeVec
}

func newV1Manager(cfg cgroupscfg.Config) *CGroupV1Manager {
	return &CGroupV1Manager{
		cfg: cfg,
		hierarchy: func() ([]cgroups.Subsystem, error) {
			return defaultSubsystems(cfg.Mountpoint)
		},
		memoryReclaimAttemptsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gitaly_cgroup_memory_reclaim_attempts_total",
				Help: "Number of memory usage hits limits",
			},
			[]string{"path"},
		),
		cpuUsage: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gitaly_cgroup_cpu_usage_total",
				Help: "CPU Usage of Cgroup",
			},
			[]string{"path", "type"},
		),
		procs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gitaly_cgroup_procs_total",
				Help: "Total number of procs",
			},
			[]string{"path", "subsystem"},
		),
	}
}

//nolint: revive,stylecheck // This is unintentionally missing documentation.
func (cg *CGroupV1Manager) Setup() error {
	var parentResources specs.LinuxResources

	if cg.cfg.CPUShares > 0 {
		parentResources.CPU = &specs.LinuxCPU{Shares: &cg.cfg.CPUShares}
	}

	if cg.cfg.MemoryBytes > 0 {
		parentResources.Memory = &specs.LinuxMemory{Limit: &cg.cfg.MemoryBytes}
	}

	if _, err := cgroups.New(
		cg.hierarchy,
		cgroups.StaticPath(cg.cfg.HierarchyRoot),
		&parentResources,
	); err != nil {
		return fmt.Errorf("failed creating parent cgroup: %w", err)
	}

	var reposResources specs.LinuxResources

	if cg.cfg.Repositories.CPUShares > 0 {
		reposResources.CPU = &specs.LinuxCPU{Shares: &cg.cfg.Repositories.CPUShares}
	}

	if cg.cfg.Repositories.MemoryBytes > 0 {
		reposResources.Memory = &specs.LinuxMemory{Limit: &cg.cfg.Repositories.MemoryBytes}
	}

	for i := 0; i < int(cg.cfg.Repositories.Count); i++ {
		if _, err := cgroups.New(
			cg.hierarchy,
			cgroups.StaticPath(cg.repoPath(i)),
			&reposResources,
		); err != nil {
			return fmt.Errorf("failed creating repository cgroup: %w", err)
		}
	}

	return nil
}

// AddCommand adds the given command to one of the CGroup's buckets. The bucket used for the command
// is determined by hashing the repository storage and path. No error is returned if the command has already
// exited.
func (cg *CGroupV1Manager) AddCommand(
	cmd *command.Command,
	repo repository.GitRepo,
) (string, error) {
	var key string
	if repo == nil {
		key = strings.Join(cmd.Args(), "/")
	} else {
		key = repo.GetStorageName() + "/" + repo.GetRelativePath()
	}

	checksum := crc32.ChecksumIEEE(
		[]byte(key),
	)

	groupID := uint(checksum) % cg.cfg.Repositories.Count
	cgroupPath := cg.repoPath(int(groupID))

	return cgroupPath, cg.addToCgroup(cmd.Pid(), cgroupPath)
}

func (cg *CGroupV1Manager) addToCgroup(pid int, cgroupPath string) error {
	control, err := cgroups.Load(cg.hierarchy, cgroups.StaticPath(cgroupPath))
	if err != nil {
		return fmt.Errorf("failed loading %s cgroup: %w", cgroupPath, err)
	}

	if err := control.Add(cgroups.Process{Pid: pid}); err != nil {
		// Command could finish so quickly before we can add it to a cgroup, so
		// we don't consider it an error.
		if strings.Contains(err.Error(), "no such process") {
			return nil
		}
		return fmt.Errorf("failed adding process to cgroup: %w", err)
	}

	return nil
}

// Collect collects metrics from the cgroups controller
func (cg *CGroupV1Manager) Collect(ch chan<- prometheus.Metric) {
	if !cg.cfg.MetricsEnabled {
		return
	}

	for i := 0; i < int(cg.cfg.Repositories.Count); i++ {
		repoPath := cg.repoPath(i)
		logger := log.Default().WithField("cgroup_path", repoPath)
		control, err := cgroups.Load(
			cg.hierarchy,
			cgroups.StaticPath(repoPath),
		)
		if err != nil {
			logger.WithError(err).Warn("unable to load cgroup controller")
			return
		}

		if metrics, err := control.Stat(); err != nil {
			logger.WithError(err).Warn("unable to get cgroup stats")
		} else {
			memoryMetric := cg.memoryReclaimAttemptsTotal.WithLabelValues(repoPath)
			memoryMetric.Set(float64(metrics.Memory.Usage.Failcnt))
			ch <- memoryMetric

			cpuUserMetric := cg.cpuUsage.WithLabelValues(repoPath, "user")
			cpuUserMetric.Set(float64(metrics.CPU.Usage.User))
			ch <- cpuUserMetric

			cpuKernelMetric := cg.cpuUsage.WithLabelValues(repoPath, "kernel")
			cpuKernelMetric.Set(float64(metrics.CPU.Usage.Kernel))
			ch <- cpuKernelMetric
		}

		if subsystems, err := cg.hierarchy(); err != nil {
			logger.WithError(err).Warn("unable to get cgroup hierarchy")
		} else {
			for _, subsystem := range subsystems {
				processes, err := control.Processes(subsystem.Name(), true)
				if err != nil {
					logger.WithField("subsystem", subsystem.Name()).
						WithError(err).
						Warn("unable to get process list")
					continue
				}

				procsMetric := cg.procs.WithLabelValues(repoPath, string(subsystem.Name()))
				procsMetric.Set(float64(len(processes)))
				ch <- procsMetric
			}
		}
	}
}

// Describe describes the cgroup metrics that Collect provides
func (cg *CGroupV1Manager) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(cg, ch)
}

// ErrProcessesExist is returned from Cleanup and indicates there are existing
// processes in the cgroup
var ErrProcessesExist = errors.New("processes exist")

//nolint: revive,stylecheck // This is unintentionally missing documentation.
func (cg *CGroupV1Manager) Cleanup() error {
	control, err := cgroups.Load(cg.hierarchy, cgroups.StaticPath(cg.cfg.HierarchyRoot))
	if err != nil {
		return fmt.Errorf("failed loading cgroup %s: %w", cg.cfg.HierarchyRoot, err)
	}

	memProcesses, err := control.Processes("memory", true)
	if err != nil {
		return fmt.Errorf("failed getting cgroup processes %s: %w", cg.cfg.HierarchyRoot, err)
	}

	cpuProcesses, err := control.Processes("cpu", true)
	if err != nil {
		return fmt.Errorf("failed getting cgroup processes %s: %w", cg.cfg.HierarchyRoot, err)
	}

	if len(memProcesses)+len(cpuProcesses) > 0 {
		return ErrProcessesExist
	}

	if err := control.Delete(); err != nil {
		return fmt.Errorf("failed cleaning up cgroup %s: %w", cg.cfg.HierarchyRoot, err)
	}

	return nil
}

func (cg *CGroupV1Manager) repoPath(groupID int) string {
	return fmt.Sprintf("%s/repos-%d", cg.cfg.HierarchyRoot, groupID)
}

func defaultSubsystems(root string) ([]cgroups.Subsystem, error) {
	subsystems := []cgroups.Subsystem{
		cgroups.NewMemory(root, cgroups.OptionalSwap()),
		cgroups.NewCpu(root),
	}

	return subsystems, nil
}
