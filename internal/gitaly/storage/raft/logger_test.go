package raft

import (
	"testing"

	dragonboatLogger "github.com/lni/dragonboat/v4/logger"
	"github.com/lni/dragonboat/v4/raftio"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestRaftLogger(t *testing.T) {
	t.Parallel()

	newLogger := func(t *testing.T) (*raftLogger, testhelper.LoggerHook) {
		logger := testhelper.NewLogger(t)
		hook := testhelper.AddLoggerHook(logger)
		hook.Reset()
		return &raftLogger{name: "test", Logger: logger}, hook
	}

	t.Run("SetLevel", func(t *testing.T) {
		logger, hook := newLogger(t)

		logger.SetLevel(dragonboatLogger.WARNING)
		require.Equal(t, dragonboatLogger.WARNING, logger.level)

		logger.Errorf("should display %s", "error")
		logger.Warningf("should display %s", "warning")
		logger.Infof("should not display %s", "info")
		logger.Debugf("should not display %s", "debug")

		entries := hook.AllEntries()
		require.Equal(t, 2, len(entries))
		require.Equal(t, "should display error", entries[0].Message)
		require.Equal(t, "should display warning", entries[1].Message)

		hook.Reset()
		logger.SetLevel(dragonboatLogger.DEBUG)
		require.Equal(t, dragonboatLogger.DEBUG, logger.level)

		logger.Errorf("should display %s", "error")
		logger.Warningf("should display %s", "warning")
		logger.Infof("should display %s", "info")
		logger.Debugf("should display display %s because test logger's level is INFO", "debug")

		entries = hook.AllEntries()
		require.Equal(t, 3, len(entries))
		require.Equal(t, "should display error", entries[0].Message)
		require.Equal(t, "should display warning", entries[1].Message)
		require.Equal(t, "should display info", entries[2].Message)
	})

	t.Run("LeaderUpdated", func(t *testing.T) {
		logger, hook := newLogger(t)

		info := raftio.LeaderInfo{
			ShardID:   123,
			ReplicaID: 456,
			LeaderID:  789,
			Term:      10,
		}
		logger.LeaderUpdated(info)

		entries := hook.AllEntries()
		require.Equal(t, 1, len(entries))
		require.Equal(t, "leader updated", entries[0].Message)
		require.Equal(t, log.Fields{
			"group_id":   info.ShardID,
			"replica_id": info.ReplicaID,
			"leader_id":  info.LeaderID,
			"term":       info.Term,
		}, entries[0].Data)
	})
}
