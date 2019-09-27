package dontpanic_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/dontpanic"
)

func TestTry(t *testing.T) {
	expectErr := errors.New("monkey wrench")
	actualErr := dontpanic.Try(func() { panic(expectErr) })
	require.Exactly(t, expectErr, actualErr)
}

func TestGo(t *testing.T) {
	expectErr := errors.New("monkey wrench")
	recoverQ := dontpanic.Go(func() { panic(expectErr) })
	for actualErr := range recoverQ {
		require.Exactly(t, expectErr, actualErr)
	}
}

func TestGoNoConsume(t *testing.T) {
	done := make(chan struct{})
	dontpanic.Go(func() { close(done) })
	<-done
}

func ExampleGoRetry() {
	const runs = 2
	var i int

	fn := func(stop func()) {
		i++
		if i > runs {
			stop()
		}
		panic(i)
	}

	defer logrus.SetOutput(os.Stderr)
	logrus.SetOutput(os.Stdout)

	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: "-", // make log messages deterministic
	})

	chainedHandlers := dontpanic.BackOff(time.Millisecond,
		dontpanic.DebugTrace(
			dontpanic.ErrorLog(nil),
		),
	)

	<-dontpanic.GoRetry(fn, chainedHandlers)

	// Output:
	// time=- level=error msg="dontpanic: panic handled: 1"
	// time=- level=info msg="dontpanic: backing off 1ms until next retry"
	// time=- level=error msg="dontpanic: panic handled: 2"
	// time=- level=info msg="dontpanic: backing off 1ms until next retry"
}
