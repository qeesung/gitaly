package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git/smudge"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
	gitalylog "gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/labkit/log"
)

func requireStdin(msg string) {
	var out string

	stat, err := os.Stdin.Stat()
	if err != nil {
		out = fmt.Sprintf("Cannot read from STDIN. %s (%s)", msg, err)
	} else if (stat.Mode() & os.ModeCharDevice) != 0 {
		out = fmt.Sprintf("Cannot read from STDIN. %s", msg)
	}

	if len(out) > 0 {
		fmt.Println(out)
		os.Exit(1)
	}
}

func main() {
	requireStdin("This command should be run by the Git 'smudge' filter")

	closer, err := initLogging(os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing log file for gitaly-lfs-smudge: %v", err)
	}
	defer closer.Close()

	if err := run(os.Environ(), os.Stdout, os.Stdin); err != nil {
		log.WithError(err).Error(err)
		os.Exit(1)
	}
}

func initLogging(environment []string) (io.Closer, error) {
	path := env.ExtractValue(environment, gitalylog.GitalyLogDirEnvKey)
	if path == "" {
		return log.Initialize(log.WithWriter(io.Discard))
	}

	filepath := filepath.Join(path, "gitaly_lfs_smudge.log")

	return log.Initialize(
		log.WithFormatter("json"),
		log.WithLogLevel("info"),
		log.WithOutputName(filepath),
	)
}

func run(environment []string, out io.Writer, in io.Reader) error {
	cfg, err := smudge.ConfigFromEnvironment(environment)
	if err != nil {
		return fmt.Errorf("loading configuration: %w", err)
	}

	if err := smudgeContents(cfg, out, in); err != nil {
		return fmt.Errorf("running smudge filter: %w", err)
	}

	return nil
}
