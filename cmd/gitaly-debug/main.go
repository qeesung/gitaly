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
	usage = "Usage: gitaly-debug test-http-clone-speed GIT_DIR"
)

func main() {
	if len(os.Args) != 3 {
		fatal(usage)
	}
	gitDir := os.Args[2]

	switch os.Args[1] {
	case "test-http-clone-speed":
		testHttpCloneSpeed(gitDir)
	default:
		fatal(usage)
	}
}

func testHttpCloneSpeed(gitDir string) {
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

	fmt.Printf("advertise-refs %d lines %v\n", len(infoLines), time.Since(start))

	if len(infoLines) < 2 {
		fatal("too few refs")
	}

	request := &bytes.Buffer{}
	refsHeads := regexp.MustCompile(`^[a-f0-9]{44} refs/heads/`)
	infoLines = infoLines[1:] // skip line with server capability advertisement
	firstLine := true
	for _, line := range infoLines {
		if !refsHeads.MatchString(line) {
			continue
		}

		commitId := line[4:44]

		if firstLine {
			firstLine = false
			fmt.Fprintf(request, "003cwant %s thin-pack\n", commitId)
			continue
		}

		fmt.Fprintf(request, "0032want %s\n", commitId)
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

	fmt.Printf("upload-pack %s %v\n", humanBytes(n), time.Since(start))
}

func noError(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(a interface{}) {
	fmt.Fprintln(os.Stderr, a)
	os.Exit(1)
}

const (
	_ = 1 << (10 * iota)
	KiB
	MiB
	GiB
	TiB
)

func humanBytes(n int64) string {
	if n > TiB {
		return fmt.Sprintf("%.2f TB", float32(n)/float32(TiB))
	}
	if n > GiB {
		return fmt.Sprintf("%.2f GB", float32(n)/float32(GiB))
	}
	if n > MiB {
		return fmt.Sprintf("%.2f MB", float32(n)/float32(MiB))
	}
	if n > KiB {
		return fmt.Sprintf("%.2f KB", float32(n)/float32(KiB))
	}
	return fmt.Sprintf("%d bytes", n)
}
