package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"time"
)

const (
	usage = `Usage: gitaly-debug SUBCOMMAND ARGS

Subcommands:

test-http-clone-speed GIT_DIR
	Simulates the server side workload of serving a full git clone over
	HTTP. The clone data is written to /dev/null. Note that in real life
	the workload also depends on the transport capabilities requested by
	the client; this tool uses a fixed set of capabilities.
`
)

func main() {
	if len(os.Args) != 3 {
		fatal(usage)
	}
	gitDir := os.Args[2]

	switch os.Args[1] {
	case "test-http-clone-speed":
		testHTTPCloneSpeed(gitDir)
	default:
		fatal(usage)
	}
}

func testHTTPCloneSpeed(gitDir string) {
	msg("Generating server response for HTTP clone. Data goes to /dev/null.")
	infoRefs := exec.Command("git", "upload-pack", "--stateless-rpc", "--advertise-refs", gitDir)
	infoRefs.Stderr = os.Stderr
	out, err := infoRefs.StdoutPipe()
	noError(err)

	start := time.Now()
	noError(infoRefs.Start())

	infoScanner := bufio.NewScanner(out)
	var infoLines []string
	for infoScanner.Scan() {
		infoLines = append(infoLines, infoScanner.Text())
	}
	noError(infoScanner.Err())

	noError(infoRefs.Wait())

	msg("advertise-refs %d lines %v", len(infoLines), time.Since(start))

	if len(infoLines) == 0 {
		fatal("no refs were advertised")
	}

	request := &bytes.Buffer{}
	refsHeads := regexp.MustCompile(`^[a-f0-9]{44} refs/heads/`)
	firstLine := true
	for _, line := range infoLines {
		if !refsHeads.MatchString(line) {
			continue
		}

		commitID := line[4:44]

		if firstLine {
			firstLine = false
			fmt.Fprintf(request, "0098want %s multi_ack_detailed no-done side-band-64k thin-pack ofs-delta deepen-since deepen-not agent=git/2.19.1\n", commitID)
			continue
		}

		fmt.Fprintf(request, "0032want %s\n", commitID)
	}
	fmt.Fprint(request, "00000009done\n")

	uploadPack := exec.Command("git", "upload-pack", "--stateless-rpc", gitDir)
	uploadPack.Stdin = request
	uploadPack.Stderr = os.Stderr
	out, err = uploadPack.StdoutPipe()
	noError(err)

	start = time.Now()
	noError(uploadPack.Start())

	n, err := io.Copy(ioutil.Discard, out)
	noError(err)

	msg("upload-pack %s %v", humanBytes(n), time.Since(start))
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
