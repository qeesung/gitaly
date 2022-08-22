package cgroups

import (
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
)

// Manager supplies an interface for interacting with cgroups
type Manager interface {
	// Setup creates cgroups and assigns configured limitations.
	// It is expected to be called once at Gitaly startup from any
	// instance of the Manager.
	Setup() error
	// AddCommand adds a Command to a cgroup.
	AddCommand(*command.Command, repository.GitRepo) (string, error)
	// Cleanup cleans up cgroups created in Setup.
	// It is expected to be called once at Gitaly shutdown from any
	// instance of the Manager.
	Cleanup() error
	Describe(ch chan<- *prometheus.Desc)
	Collect(ch chan<- prometheus.Metric)
}

// NewManager returns the appropriate Cgroups manager
func NewManager(cfg cgroups.Config, pid int) Manager {
	if cfg.Repositories.Count > 0 {
		return newV1Manager(cfg, pid)
	}

	return &NoopManager{}
}

// PruneOldCgroups prunes old cgroups for both the memory and cpu subsystems
func PruneOldCgroups(cfg cgroups.Config, logger log.FieldLogger) {
	if cfg.HierarchyRoot == "" {
		return
	}

	if err := config.PruneOldGitalyProcessDirectories(
		logger,
		filepath.Join(cfg.Mountpoint, "memory",
			cfg.HierarchyRoot),
	); err != nil {
		logger.WithError(err).Error("failed to clean up memory cgroups")
	}

	if err := config.PruneOldGitalyProcessDirectories(
		logger,
		filepath.Join(cfg.Mountpoint, "cpu",
			cfg.HierarchyRoot),
	); err != nil {
		logger.WithError(err).Error("failed to clean up cpu cgroups")
	}
}
