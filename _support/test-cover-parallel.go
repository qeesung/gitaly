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

func main() {
	if len(os.Args) <= 2 {
		log.Fatalf("usage %s OUT_DIR PKG [PKG...]", progName)
	}

	outDir := os.Args[1]
	packages := os.Args[2:]

	if err := buildDependentPackages(packages); err != nil {
		log.Fatal(err)
	}

	numWorkers := 2
	packageChan := make(chan string)
	wg := &sync.WaitGroup{}
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			for p := range packageChan {
				handlePackage(p, outDir, packages)
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

func buildDependentPackages(packages []string) error {
	buildDeps := exec.Command("go", append([]string{"test", "-i"}, packages...)...)
	buildDeps.Stdout = os.Stdout
	buildDeps.Stderr = os.Stderr
	start := time.Now()
	if err := buildDeps.Run(); err != nil {
		log.Printf("command failed: %s", strings.Join(buildDeps.Args, " "))
		return err
	}
	log.Printf("go test -i\t%.3fs", time.Since(start).Seconds())
	return nil
}

func handlePackage(pkg string, outDir string, packages []string) {
	args := []string{
		"go",
		"test",
		fmt.Sprintf("-coverpkg=%s", strings.Join(packages, ",")),
		fmt.Sprintf("-coverprofile=%s/unit-%s.out", outDir, strings.Replace(pkg, "/", "_", -1)),
		pkg,
	}

	cmd := exec.Command(args[0], args[1:]...)

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	status := fmt.Sprintf("%s\t%.3fs", pkg, duration.Seconds())

	if err != nil {
		fmt.Printf("FAIL\t%s\n", status)
		fmt.Printf("command was: %s\n", strings.Join(cmd.Args, " "))
	} else {
		fmt.Printf("ok  \t%s\n", status)
	}
}
