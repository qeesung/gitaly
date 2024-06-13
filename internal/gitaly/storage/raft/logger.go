package raft

import (
	"fmt"
	"strings"
	"sync"

	dragonboatLogger "github.com/lni/dragonboat/v4/logger"
	"github.com/lni/dragonboat/v4/raftio"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

var setLoggerOnce sync.Once

// raftLogger implements dragonboat's logger interface.
type raftLogger struct {
	log.Logger
	name  string
	level dragonboatLogger.LogLevel
}

func (l *raftLogger) SetLevel(level dragonboatLogger.LogLevel) {
	l.level = level
}

func (l *raftLogger) Debugf(msg string, args ...any) {
	if l.level >= dragonboatLogger.DEBUG {
		l.Debug(fmt.Sprintf(msg, args...))
	}
}

func (l *raftLogger) Infof(msg string, args ...any) {
	if l.level >= dragonboatLogger.INFO {
		l.Info(fmt.Sprintf(msg, args...))
	}
}

func (l *raftLogger) Warningf(msg string, args ...any) {
	if l.level >= dragonboatLogger.WARNING {
		l.Warn(fmt.Sprintf(msg, args...))
	}
}

func (l *raftLogger) Errorf(msg string, args ...any) {
	if l.level >= dragonboatLogger.ERROR {
		l.Error(fmt.Sprintf(msg, args...))
	}
}

func (l *raftLogger) Panicf(msg string, args ...any) {
	msg = fmt.Sprintf(msg, args...)
	l.Error(msg)
}

func (l *raftLogger) LeaderUpdated(info raftio.LeaderInfo) {
	l.WithFields(log.Fields{
		"group_id":   info.ShardID,
		"replica_id": info.ReplicaID,
		"leader_id":  info.LeaderID,
		"term":       info.Term,
	}).Info("leader updated")
}

var _ = raftio.IRaftEventListener(&raftLogger{})

// SetLogger replaces dragonboat's package by Gitaly's configured package. It should be called once.
// Consequent calls are ignored.
func SetLogger(logger log.Logger, suppressDefaultLog bool) {
	setLoggerOnce.Do(func() {
		// Replace logger factory.
		dragonboatLogger.SetLoggerFactory(func(pkgName string) dragonboatLogger.ILogger {
			return &raftLogger{
				name: pkgName,
				Logger: logger.WithFields(log.Fields{
					"component":      "raft",
					"raft_component": strings.ToLower(pkgName),
				}),
			}
		})
		// Suppress some chatty components. We lift up the default level. When logs are outputted,
		// they are also controlled by Gitaly's logger configuration.
		if suppressDefaultLog {
			dragonboatLogger.GetLogger("dragonboat").SetLevel(dragonboatLogger.WARNING)
			dragonboatLogger.GetLogger("raft").SetLevel(dragonboatLogger.ERROR)
			dragonboatLogger.GetLogger("rsm").SetLevel(dragonboatLogger.WARNING)
			dragonboatLogger.GetLogger("transport").SetLevel(dragonboatLogger.ERROR)
			dragonboatLogger.GetLogger("grpc").SetLevel(dragonboatLogger.ERROR)
		} else {
			dragonboatLogger.GetLogger("dragonboat").SetLevel(dragonboatLogger.INFO)
			dragonboatLogger.GetLogger("raft").SetLevel(dragonboatLogger.INFO)
			dragonboatLogger.GetLogger("rsm").SetLevel(dragonboatLogger.INFO)
			dragonboatLogger.GetLogger("transport").SetLevel(dragonboatLogger.INFO)
			dragonboatLogger.GetLogger("grpc").SetLevel(dragonboatLogger.INFO)
		}
	})
}
