package raft

import (
	"fmt"
	"strings"
	"sync"

	dragonboatLogger "github.com/lni/dragonboat/v4/logger"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

var setLoggerOnce sync.Once

type raftLogger struct {
	log.Logger
}

func (l raftLogger) SetLevel(dragonboatLogger.LogLevel) {
}

func (l raftLogger) Debugf(msg string, args ...any) {
	l.Debug(fmt.Sprintf(msg, args...))
}

func (l raftLogger) Infof(msg string, args ...any) {
	l.Info(fmt.Sprintf(msg, args...))
}

func (l raftLogger) Warningf(msg string, args ...any) {
	l.Warn(fmt.Sprintf(msg, args...))
}

func (l raftLogger) Errorf(msg string, args ...any) {
	l.Error(fmt.Sprintf(msg, args...))
}

func (l raftLogger) Panicf(msg string, args ...any) {
	msg = fmt.Sprintf(msg, args...)
	l.Error(msg)
	panic(msg)
}

func SetLogger(logger log.Logger) {
	setLoggerOnce.Do(func() {
		dragonboatLogger.SetLoggerFactory(func(pkgName string) dragonboatLogger.ILogger {
			return &raftLogger{
				Logger: logger.WithFields(log.Fields{
					"component":      "raft",
					"raft_component": strings.ToLower(pkgName),
				}),
			}
		})
	})
}
