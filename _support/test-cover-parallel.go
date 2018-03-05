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
	cmdChan := make(chan *exec.Cmd)
	wg := &sync.WaitGroup{}
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			for cmd := range cmdChan {
				runCover(cmd)
			}
			wg.Done()
		}()
	}

	packageMap := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		packageMap[pkg] = true
	}

	for _, pkg := range packages {
		deps, err := depsForPackage(pkg, packageMap)
		if err != nil {
			log.Fatal(err)
		}

		args := []string{
			"go",
			"test",
			fmt.Sprintf("-coverpkg=%s", strings.Join(deps, ",")),
			fmt.Sprintf("-coverprofile=%s/unit-%s.out", outDir, strings.Replace(pkg, "/", "_", -1)),
			pkg,
		}

		cmdChan <- exec.Command(args[0], args[1:]...)
	}
	close(cmdChan)

	wg.Wait()
}

func depsForPackage(pkg string, packageMap map[string]bool) ([]string, error) {
	depsOut, err := exec.Command("go", "list", "-f", `{{ join .Deps "\n" }}`, pkg).Output()
	if err != nil {
		return nil, err
	}

	deps := []string{pkg}
	for _, d := range strings.Split(string(depsOut), "\n") {
		if packageMap[d] {
			deps = append(deps, d)
		}
	}

	return deps, nil
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

func runCover(cmd *exec.Cmd) {
	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	status := fmt.Sprintf("%s\t%.3fs", cmd.Args[len(cmd.Args)-1], duration.Seconds())

	if err != nil {
		fmt.Printf("FAIL\t%s\n", status)
		fmt.Printf("command was: %s\n", strings.Join(cmd.Args, " "))
	} else {
		fmt.Printf("ok  \t%s\n", status)
	}
}
