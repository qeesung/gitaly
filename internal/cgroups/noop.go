package cgroups

import (
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
)

// NoopManager is a cgroups manager that does nothing
type NoopManager struct{}

//nolint: stylecheck // This is unintentionally missing documentation.
func (cg *NoopManager) Setup() error {
	return nil
}

//nolint: stylecheck // This is unintentionally missing documentation.
func (cg *NoopManager) AddCommand(cmd *command.Command, repo repository.GitRepo) (string, error) {
	return "", nil
}

//nolint: stylecheck // This is unintentionally missing documentation.
func (cg *NoopManager) Cleanup() error {
	return nil
}

// Describe does nothing
func (cg *NoopManager) Describe(ch chan<- *prometheus.Desc) {}

// Collect does nothing
func (cg *NoopManager) Collect(ch chan<- prometheus.Metric) {}
