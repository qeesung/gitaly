package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v2"

	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	grpccorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	grpctracing "gitlab.com/gitlab-org/labkit/tracing/grpc"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := client.Dial(os.Getenv("GITALY_SOCKET"), dialOpts())
	if err != nil {
		logger.Fatalf("error when dialing: %v", err)
	}

	c := gitalypb.NewHookServiceClient(conn)

	switch subCmd {
	case "update":
		args := os.Args[2:]
		if len(args) != 3 {
			logger.Fatal(errors.New("update hook missing required arguments"))
		}
		ref, oldValue, newValue := args[0], args[1], args[2]

		req := &gitalypb.UpdateHookRequest{
			Repository: &gitalypb.Repository{
				StorageName:  os.Getenv("GL_REPO_STORAGE"),
				RelativePath: os.Getenv("GL_REPO_RELATIVE_PATH"),
				GlRepository: os.Getenv("GL_REPOSITORY"),
			},
			KeyId:    os.Getenv("GL_ID"),
			Ref:      []byte(ref),
			OldValue: oldValue,
			NewValue: newValue,
		}

		resp, err := c.UpdateHook(ctx, req)
		if err != nil {
			os.Exit(1)
		}

		io.Copy(os.Stdout, bytes.NewBuffer(resp.GetStdout()))
		io.Copy(os.Stderr, bytes.NewBuffer(resp.GetStderr()))

		if !resp.GetSuccess() {
			os.Exit(1)
		}
	case "pre-receive":
		stdin, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			logger.Fatalf("error when copying from stdin: %v", err)
		}

		req := &gitalypb.PreReceiveHookRequest{
			Repository: &gitalypb.Repository{
				StorageName:  os.Getenv("GL_REPO_STORAGE"),
				RelativePath: os.Getenv("GL_REPO_RELATIVE_PATH"),
				GlRepository: os.Getenv("GL_REPOSITORY"),
			},
			KeyId:    os.Getenv("GL_ID"),
			Protocol: os.Getenv("GL_PROTOCOL"),
			Stdin:    stdin,
		}

		resp, err := c.PreReceiveHook(ctx, req)
		if err != nil {
			os.Exit(1)
		}

		io.Copy(os.Stdout, bytes.NewBuffer(resp.GetStdout()))
		io.Copy(os.Stderr, bytes.NewBuffer(resp.GetStderr()))

		if !resp.GetSuccess() {
			os.Exit(1)
		}
	case "post-receive":
		stdin, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			logger.Fatalf("error when copying from stdin: %v", err)
		}

		req := &gitalypb.PostReceiveHookRequest{
			Repository: &gitalypb.Repository{
				StorageName:  os.Getenv("GL_REPO_STORAGE"),
				RelativePath: os.Getenv("GL_REPO_RELATIVE_PATH"),
				GlRepository: os.Getenv("GL_REPOSITORY"),
			},
			KeyId: os.Getenv("GL_ID"),
			Stdin: stdin,
		}

		resp, err := c.PostReceiveHook(ctx, req)
		if err != nil {
			os.Exit(1)
		}

		io.Copy(os.Stdout, bytes.NewBuffer(resp.GetStdout()))
		io.Copy(os.Stderr, bytes.NewBuffer(resp.GetStderr()))

		if !resp.GetSuccess() {
			os.Exit(1)
		}
	default:
		logger.Fatal(errors.New("hook name invalid"))
	}
}

func dialOpts() []grpc.DialOption {
	connOpts := client.DefaultDialOpts
	connOpts = append(connOpts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentials(config.Config.Auth.Token)))

	// Add grpc client interceptors
	connOpts = append(connOpts, grpc.WithStreamInterceptor(
		grpc_middleware.ChainStreamClient(
			grpctracing.StreamClientTracingInterceptor(),         // Tracing
			grpccorrelation.StreamClientCorrelationInterceptor(), // Correlation
		)),

		grpc.WithUnaryInterceptor(
			grpc_middleware.ChainUnaryClient(
				grpctracing.UnaryClientTracingInterceptor(),         // Tracing
				grpccorrelation.UnaryClientCorrelationInterceptor(), // Correlation
			)))

	return connOpts
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
