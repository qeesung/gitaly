package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gopkg.in/yaml.v2"
)

func main() {
	var logger = log.NewHookLogger()

	if len(os.Args) < 2 {
		logger.Fatal(errors.New("requires hook name"))
	}

	subCmd := os.Args[1]

	if subCmd == "check" {
		configPath := os.Args[2]

		if err := checkGitlabAccess(configPath); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}

		os.Stdout.WriteString("OK")
		os.Exit(0)
	}

	gitlabRubyDir := os.Getenv("GITALY_RUBY_DIR")
	if gitlabRubyDir == "" {
		logger.Fatal(errors.New("GITALY_RUBY_DIR not set"))
	}

	rubyHookPath := filepath.Join(gitlabRubyDir, "gitlab-shell", "hooks", subCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var hookCmd *exec.Cmd

	switch subCmd {
	case "update":
		args := os.Args[2:]
		if len(args) != 3 {
			logger.Fatal(errors.New("update hook missing required arguments"))
		}

		hookCmd = exec.Command(rubyHookPath, args...)
	case "pre-receive", "post-receive":
		hookCmd = exec.Command(rubyHookPath)

	default:
		logger.Fatal(errors.New("hook name invalid"))
	}

	cmd, err := command.New(ctx, hookCmd, os.Stdin, os.Stdout, os.Stderr, os.Environ()...)
	if err != nil {
		logger.Fatalf("error when starting command for %v: %v", rubyHookPath, err)
	}

	if err = cmd.Wait(); err != nil {
		os.Exit(1)
	}
}

// GitlabShellConfig contains a subset of gitlabshell's config.yml
type GitlabShellConfig struct {
	GitlabURL    string       `yaml:"gitlab_url"`
	HTTPSettings HTTPSettings `yaml:"http_settings"`
}

// HTTPSettings contains fields for http settings
type HTTPSettings struct {
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

func checkGitlabAccess(configPath string) error {
	cfgFile, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("error when opening config file: %v", err)
	}
	defer cfgFile.Close()

	config := GitlabShellConfig{}

	if err := yaml.NewDecoder(cfgFile).Decode(&config); err != nil {
		return fmt.Errorf("load toml: %v", err)
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v4/internal/check", strings.TrimRight(config.GitlabURL, "/")), nil)
	if err != nil {
		return fmt.Errorf("could not create request for %s: %v", config.GitlabURL, err)
	}

	req.SetBasicAuth(config.HTTPSettings.User, config.HTTPSettings.Password)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error with request for %s: %v", config.GitlabURL, err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("FAILED. code: %d", resp.StatusCode)
	}

	return nil
}
