package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
)

func execCheck() {
	logrus.SetLevel(logrus.ErrorLevel)

	checkCmd := flag.NewFlagSet("check", flag.ExitOnError)
	checkCmd.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %v check <configfile>\n", os.Args[0])
		checkCmd.PrintDefaults()
	}

	_ = checkCmd.Parse(os.Args[2:])

	if checkCmd.NArg() != 1 || checkCmd.Arg(0) == "" {
		fmt.Fprintf(os.Stderr, "error: invalid argument(s)")
		checkCmd.Usage()
		os.Exit(2)
	}

	configPath := checkCmd.Arg(0)

	fmt.Print("Checking GitLab API access: ")
	info, err := checkAPI(configPath)
	if err != nil {
		fmt.Println("FAILED")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println("OK")
	fmt.Printf("GitLab version: %s\n", info.Version)
	fmt.Printf("GitLab revision: %s\n", info.Revision)
	fmt.Printf("GitLab Api version: %s\n", info.APIVersion)
	fmt.Printf("Redis reachable for GitLab: %t\n", info.RedisReachable)
	fmt.Println("OK")

	os.Exit(0)
}

func checkAPI(configPath string) (*gitlab.CheckInfo, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: config_path %q: %w", configPath, err)
	}

	gitlabAPI, err := gitlab.NewHTTPClient(logrus.New(), cfg.Gitlab, cfg.TLS, prometheus.Config{})
	if err != nil {
		return nil, err
	}

	gitCmdFactory, cleanup, err := git.NewExecCommandFactory(cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return hook.NewManager(cfg, config.NewLocator(cfg), gitCmdFactory, nil, gitlabAPI).Check(context.Background())
}
