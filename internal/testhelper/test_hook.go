package testhelper

import (
	"io/ioutil"
	"testing"

	log "github.com/sirupsen/logrus"
)

// NewTestLogger created a logrus hook
func NewTestLogger(tb testing.TB) *log.Logger {
	logger := log.New()
	logger.Out = ioutil.Discard

	return logger
}
