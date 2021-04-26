package cgroups

import (
	"gitlab.com/gitlab-org/gitaly/internal/command"
)

// NoopManager is a cgroups manager that does nothing
type NoopManager struct{}

// Setup does nothing.
func (cg *NoopManager) Setup() error {
	return nil
}

// AddCommand does nothing.
func (cg *NoopManager) AddCommand(cmd *command.Command) error {
	return nil
}

// Cleanup does nothing.
func (cg *NoopManager) Cleanup() error {
	return nil
}
