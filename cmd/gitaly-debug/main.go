package main

import (
	"fmt"
	"os"
)

const (
	usage = `Usage: gitaly-debug SUBCOMMAND ARGS

Subcommands:

simulate-http-clone GIT_DIR
	Simulates the server side workload of serving a full git clone over
	HTTP. The clone data is written to /dev/null. Note that in real life
	the workload also depends on the transport capabilities requested by
	the client; this tool uses a fixed set of capabilities.

analyze-http-clone HTTP_URL
	Clones a Git repository from a public HTTP URL into /dev/null.
`
)

func main() {
	if len(os.Args) < 2 {
		fatal(usage)
	}
	extraArgs := os.Args[2:]

	switch os.Args[1] {
	case "simulate-http-clone":
		if len(extraArgs) != 1 {
			fatal(usage)
		}
		simulateHTTPClone(extraArgs[0])
	case "analyze-http-clone":
		if len(extraArgs) != 1 {
			fatal(usage)
		}
		analyzeHTTPClone(extraArgs[0])
	default:
		fatal(usage)
	}
}

func noError(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(a interface{}) {
	msg("%v", a)
	os.Exit(1)
}

func msg(format string, a ...interface{}) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(format, a...))
}

func humanBytes(n int64) string {
	units := []struct {
		size  int64
		label string
	}{
		{size: 1000000000000, label: "TB"},
		{size: 1000000000, label: "GB"},
		{size: 1000000, label: "MB"},
		{size: 1000, label: "KB"},
	}

	for _, u := range units {
		if n > u.size {
			return fmt.Sprintf("%.2f %s", float32(n)/float32(u.size), u.label)
		}
	}

	return fmt.Sprintf("%d bytes", n)
}
