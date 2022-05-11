package cgroups

import (
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config/cgroups"
)

// Manager supplies an interface for interacting with cgroups
type Manager interface {
	// Setup creates cgroups and assigns configured limitations.
	// It is expected to be called once at Gitaly startup from any
	// instance of the Manager.
	Setup() error
	// AddCommand adds a Command to a cgroup
	AddCommand(*command.Command) error
	// Cleanup cleans up cgroups created in Setup.
	// It is expected to be called once at Gitaly shutdown from any
	// instance of the Manager.
	Cleanup() error
	Describe(ch chan<- *prometheus.Desc)
	Collect(ch chan<- prometheus.Metric)
}

// NewManager returns the appropriate Cgroups manager
func NewManager(cfg cgroups.Config) Manager {
	// nolint:staticcheck // we will deprecate the old cgroups config in 15.0
	if cfg.Count > 0 {
		return newV1Manager(cfg)
	}

	return &NoopManager{}
}
