package testhelper

import (
	"io/ioutil"
	"testing"

	log "github.com/sirupsen/logrus"
)

// NewTestLogger creates logger that should be used in the tests.
var NewTestLogger = DiscardTestLogger

// DiscardTestLogger created a logrus hook that discards everything.
func DiscardTestLogger(tb testing.TB) *log.Logger {
	logger := log.New()
	logger.Out = ioutil.Discard

	return logger
}
