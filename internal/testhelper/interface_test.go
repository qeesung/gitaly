package testhelper

import (
	"testing"
)

// test that TB interface is a subset of testing.TB,
// compiling fails if this is not the case.
var _ TB = (testing.TB)(nil)
