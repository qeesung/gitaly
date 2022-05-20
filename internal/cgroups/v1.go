//go:build !linux
// +build !linux

package cgroups

import (
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
)

// For systems other than Linux, we return a noop manager if cgroups was enabled.
func newV1Manager(cfg cgroups.Config) *NoopManager {
	return &NoopManager{}
}
