package command

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

var (
	spawnTokens chan struct{}
	spawnConfig SpawnConfig
)

// SpawnConfig holds configuration for command spawning timeouts and parallelism.
type SpawnConfig struct {
	// This default value (10 seconds) is very high. Spawning should take
	// milliseconds or less. If we hit 10 seconds, something is wrong, and
	// failing the request will create breathing room. Can be modified at
	// runtime with the GITALY_COMMAND_SPAWN_TIMEOUT environment variable.
	Timeout time.Duration `split_words:"true" default:"10s"`

	// MaxSpawnParallel limits the number of goroutines that can spawn a
	// process at the same time. These parallel spawns will contend for a
	// single lock (syscall.ForkLock) in exec.Cmd.Start(). Can be modified at
	// runtime with the GITALY_COMMAND_SPAWN_MAX_PARALLEL variable.
	//
	// Note that this does not limit the total number of child processes that
	// can be attached to Gitaly at the same time. It only limits the rate at
	// which we can create new child processes.
	MaxParallel int `split_words:"true" default:"100"`
}

func init() {
	envconfig.MustProcess("gitaly_command_spawn", &spawnConfig)
	spawnTokens = make(chan struct{}, spawnConfig.MaxParallel)
}
