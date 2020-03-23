package testhelper

// TB is an interface that matches that of testing.TB.
// Using TB rather than testing.TB prevents side effects from
// importing testing package such as extra flags being added to the default
// flag set. TB should be used in testing utilities that should be
// importable by tests in other packages, namely anything that is not in a
// *_test.go file.
type TB interface {
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fail()
	FailNow()
	Failed() bool
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
	Helper()
	Log(args ...interface{})
	Logf(format string, args ...interface{})
	Name() string
	Skip(args ...interface{})
	SkipNow()
	Skipf(format string, args ...interface{})
	Skipped() bool
}
