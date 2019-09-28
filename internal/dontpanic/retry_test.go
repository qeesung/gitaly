package dontpanic_test

import (
	"errors"
	"fmt"
	"testing"

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

	panicHandler := func(r interface{}) { fmt.Println("Panic", r) }

	<-dontpanic.GoRetry(fn, panicHandler)

	// Output:
	// Panic 1
	// Panic 2
}
