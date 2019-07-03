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

func simulateHTTPClone(gitDir string) {
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

	msg("simulated GET \"/info/refs?service=git-upload-pack\" returned %d lines, took %v", len(infoLines), time.Since(start))

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

	msg("simulated POST \"/git-upload-pack\" returned %s, took %v", humanBytes(n), time.Since(start))
}
