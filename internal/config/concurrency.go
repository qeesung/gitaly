package config

import (
	"gitlab.com/gitlab-org/gitaly/internal/middleware/limithandler"
)

// ConfigureConcurrencyLimits configures the sentry DSN
func ConfigureConcurrencyLimits() {
	var maxConcurrencyPerRepoPerRPC map[string]int64

	maxConcurrencyPerRepoPerRPC = make(map[string]int64)
	for _, v := range Config.Concurrency {
		maxConcurrencyPerRepoPerRPC[v.RPC] = v.MaxPerRepo
	}

	limithandler.SetMaxRepoConcurrency(maxConcurrencyPerRepoPerRPC)
}
