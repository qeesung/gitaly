package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	progName = "test-cover-parallel.go"
)

var (
	numWorkers = 2 //runtime.GOMAXPROCS(0)
)

func main() {
	if len(os.Args) <= 2 {
		log.Fatalf("usage %s OUT_DIR PKG [PKG...]", progName)
	}

	outDir := os.Args[1]
	packages := os.Args[2:]
	baseArgs := []string{"go", "test", fmt.Sprintf("-coverpkg=%s", strings.Join(packages, ","))}

	packageChan := make(chan string)
	wg := &sync.WaitGroup{}
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			for p := range packageChan {
				handlePackage(p, outDir, baseArgs)
			}
			wg.Done()
		}()
	}

	for _, p := range packages {
		packageChan <- p
	}
	close(packageChan)

	wg.Wait()
}

func handlePackage(p string, outDir string, baseArgs []string) {
	args := baseArgs
	args = append(
		baseArgs,
		fmt.Sprintf("-coverprofile=%s/unit-%s.out", outDir, strings.Replace(p, "/", "_", -1)),
		p,
	)
	start := time.Now()
	cmd := exec.Command(args[0], args[1:]...)
	//	cmd.Stdout = os.Stdout
	//	cmd.Stderr = os.Stderr // contains too much rubbish
	err := cmd.Run()
	duration := time.Since(start)
	status := fmt.Sprintf("%s\t%.3fs", p, duration.Seconds())
	if err != nil {
		fmt.Printf("FAIL\t%s\n", status)
		fmt.Printf("command was: %s\n", strings.Join(cmd.Args, " "))
	} else {
		fmt.Printf("ok  \t%s\n", status)
	}
}
