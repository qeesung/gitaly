package config

import (
	"gitlab.com/gitlab-org/gitaly/internal/middleware/limithandler"
)

// ConfigureConcurrencyLimits configures the per-repo, per RPC rate limits
func ConfigureConcurrencyLimits() {
	maxConcurrencyPerRepoPerRPC := make(map[string]int)

	for _, v := range Config.Concurrency {
		maxConcurrencyPerRepoPerRPC[v.RPC] = v.MaxPerRepo
	}

	limithandler.SetMaxRepoConcurrency(maxConcurrencyPerRepoPerRPC)
}
