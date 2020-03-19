package testhelper

import (
	"io/ioutil"
	"os"
	"testing"

	log "github.com/sirupsen/logrus"
)

// NewTestLogger creates logger that should be used in the tests.
// It depends on the `--verbose` flag used to run the test.
var NewTestLogger = func(tb testing.TB) *log.Logger {
	if testing.Verbose() {
		return VerboseLogger()
	}
	return DiscardTestLogger()
}

func VerboseLogger() *log.Logger {
	return &log.Logger{Out: os.Stdout, Formatter: new(log.JSONFormatter), Level: log.InfoLevel}
}

// DiscardTestLogger created a logrus hook that discards everything.
func DiscardTestLogger() *log.Logger {
	logger := log.New()
	logger.Out = ioutil.Discard

	return logger
}

// DiscardTestLogger created a logrus entry that discards everything.
func DiscardTestEntry(tb testing.TB) *log.Entry {
	return log.NewEntry(DiscardTestLogger())
}

// NewTestEntry creates a logrus entry.
// It depends on the `--verbose` flag used to run the test.
var NewTestEntry = func(tb testing.TB) *log.Entry {
	return log.NewEntry(NewTestLogger(tb))
}
