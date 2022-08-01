package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
	gitalylog "gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/stream"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"gitlab.com/gitlab-org/labkit/tracing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type hookError struct {
	returnCode int
	err        error
}

func (e hookError) Error() string {
	return fmt.Sprintf("hook returned error code %d", e.returnCode)
}

type hookCommand struct {
	exec     func(context.Context, git.HooksPayload, gitalypb.HookServiceClient, []string) error
	hookType git.Hook
}

var hooksBySubcommand = map[string]hookCommand{
	"update": {
		exec:     updateHook,
		hookType: git.UpdateHook,
	},
	"pre-receive": {
		exec:     preReceiveHook,
		hookType: git.PreReceiveHook,
	},
	"post-receive": {
		exec:     postReceiveHook,
		hookType: git.PostReceiveHook,
	},
	"reference-transaction": {
		exec:     referenceTransactionHook,
		hookType: git.ReferenceTransactionHook,
	},
}

func main() {
	logger := gitalylog.NewHookLogger()

	if err := run(os.Args); err != nil {
		var hookError hookError
		if errors.As(err, &hookError) {
			if hookError.err != nil {
				logger.Fatalf("%s", err)
			}
			os.Exit(hookError.returnCode)
		}

		logger.Fatalf("%s", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch filepath.Base(args[0]) {
	case "gitaly-hooks":
		if len(args) < 2 {
			return fmt.Errorf("requires hook name. args: %v", args)
		}

		subCmd := args[1]

		switch subCmd {
		case "git":
			return executeHook(hookCommand{
				exec:     packObjectsHook,
				hookType: git.PackObjectsHook,
			}, args[2:])
		}

		return fmt.Errorf("subcommand name invalid: %q", subCmd)
	default:
		hookName := filepath.Base(args[0])
		hookCommand, ok := hooksBySubcommand[hookName]
		if !ok {
			return fmt.Errorf("subcommand name invalid: %q", hookName)
		}

		return executeHook(hookCommand, args[1:])
	}
}

func executeHook(cmd hookCommand, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Since the environment is sanitized at the moment, we're only
	// using this to extract the correlation ID. The finished() call
	// to clean up the tracing will be a NOP here.
	ctx, finished := tracing.ExtractFromEnv(ctx)
	defer finished()

	payload, err := git.HooksPayloadFromEnv(os.Environ())
	if err != nil {
		return fmt.Errorf("error when getting hooks payload: %v", err)
	}

	// If the hook wasn't requested, then we simply skip executing any
	// logic.
	if !payload.IsHookRequested(cmd.hookType) {
		return nil
	}

	conn, err := dialGitaly(payload)
	if err != nil {
		return fmt.Errorf("error when connecting to gitaly: %v", err)
	}
	defer conn.Close()

	hookClient := gitalypb.NewHookServiceClient(conn)

	for _, flag := range payload.FeatureFlagsWithValue {
		ctx = featureflag.OutgoingCtxWithFeatureFlag(ctx, flag.Flag, flag.Enabled)
	}

	if err := cmd.exec(ctx, payload, hookClient, args); err != nil {
		return err
	}

	return nil
}

func noopSender(c chan error) {}

func dialGitaly(payload git.HooksPayload) (*grpc.ClientConn, error) {
	dialOpts := client.DefaultDialOpts
	if payload.InternalSocketToken != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(payload.InternalSocketToken)))
	}

	conn, err := client.Dial("unix://"+payload.InternalSocket, dialOpts)
	if err != nil {
		return nil, fmt.Errorf("error when dialing: %w", err)
	}

	return conn, nil
}

func gitPushOptions() []string {
	var gitPushOptions []string

	gitPushOptionCount, err := env.GetInt("GIT_PUSH_OPTION_COUNT", 0)
	if err != nil {
		return gitPushOptions
	}

	for i := 0; i < gitPushOptionCount; i++ {
		gitPushOptions = append(gitPushOptions, os.Getenv(fmt.Sprintf("GIT_PUSH_OPTION_%d", i)))
	}

	return gitPushOptions
}

func sendFunc(reqWriter io.Writer, stream grpc.ClientStream, stdin io.Reader) func(errC chan error) {
	return func(errC chan error) {
		_, errSend := io.Copy(reqWriter, stdin)
		errClose := stream.CloseSend()
		if errSend != nil {
			errC <- errSend
		} else {
			errC <- errClose
		}
	}
}

func updateHook(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("update hook expects exactly three arguments, got %q", args)
	}
	ref, oldValue, newValue := args[0], args[1], args[2]

	req := &gitalypb.UpdateHookRequest{
		Repository:           payload.Repo,
		EnvironmentVariables: os.Environ(),
		Ref:                  []byte(ref),
		OldValue:             oldValue,
		NewValue:             newValue,
	}

	updateHookStream, err := hookClient.UpdateHook(ctx, req)
	if err != nil {
		return fmt.Errorf("error when starting command for update hook: %v", err)
	}

	if returnCode, err := stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return updateHookStream.Recv()
	}, noopSender, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("error when receiving data for update hook: %v", err)
	} else if returnCode != 0 {
		return hookError{returnCode: int(returnCode)}
	}

	return nil
}

func preReceiveHook(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	preReceiveHookStream, err := hookClient.PreReceiveHook(ctx)
	if err != nil {
		return fmt.Errorf("error when getting preReceiveHookStream client for: %v", err)
	}

	if err := preReceiveHookStream.Send(&gitalypb.PreReceiveHookRequest{
		Repository:           payload.Repo,
		EnvironmentVariables: os.Environ(),
		GitPushOptions:       gitPushOptions(),
	}); err != nil {
		return fmt.Errorf("error when sending request for pre-receive hook: %v", err)
	}

	f := sendFunc(streamio.NewWriter(func(p []byte) error {
		return preReceiveHookStream.Send(&gitalypb.PreReceiveHookRequest{Stdin: p})
	}), preReceiveHookStream, os.Stdin)

	if returnCode, err := stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return preReceiveHookStream.Recv()
	}, f, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("error when receiving data for pre-receive hook: %v", err)
	} else if returnCode != 0 {
		return hookError{returnCode: int(returnCode)}
	}

	return nil
}

func postReceiveHook(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	postReceiveHookStream, err := hookClient.PostReceiveHook(ctx)
	if err != nil {
		return fmt.Errorf("error when getting stream client for post-receive hook: %v", err)
	}

	if err := postReceiveHookStream.Send(&gitalypb.PostReceiveHookRequest{
		Repository:           payload.Repo,
		EnvironmentVariables: os.Environ(),
		GitPushOptions:       gitPushOptions(),
	}); err != nil {
		return fmt.Errorf("error when sending request for post-receive hook: %v", err)
	}

	f := sendFunc(streamio.NewWriter(func(p []byte) error {
		return postReceiveHookStream.Send(&gitalypb.PostReceiveHookRequest{Stdin: p})
	}), postReceiveHookStream, os.Stdin)

	if returnCode, err := stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return postReceiveHookStream.Recv()
	}, f, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("error when receiving data for post-receive hook: %v", err)
	} else if returnCode != 0 {
		return hookError{returnCode: int(returnCode)}
	}

	return nil
}

func referenceTransactionHook(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("reference-transaction hook is missing required arguments, got %q", args)
	}

	var state gitalypb.ReferenceTransactionHookRequest_State
	switch args[0] {
	case "prepared":
		state = gitalypb.ReferenceTransactionHookRequest_PREPARED
	case "committed":
		state = gitalypb.ReferenceTransactionHookRequest_COMMITTED
	case "aborted":
		state = gitalypb.ReferenceTransactionHookRequest_ABORTED
	default:
		return fmt.Errorf("reference-transaction hook has invalid state: %q", args[0])
	}

	referenceTransactionHookStream, err := hookClient.ReferenceTransactionHook(ctx)
	if err != nil {
		return fmt.Errorf("error when getting referenceTransactionHookStream client: %v", err)
	}

	if err := referenceTransactionHookStream.Send(&gitalypb.ReferenceTransactionHookRequest{
		Repository:           payload.Repo,
		EnvironmentVariables: os.Environ(),
		State:                state,
	}); err != nil {
		return fmt.Errorf("error when sending request for reference-transaction hook: %v", err)
	}

	f := sendFunc(streamio.NewWriter(func(p []byte) error {
		return referenceTransactionHookStream.Send(&gitalypb.ReferenceTransactionHookRequest{Stdin: p})
	}), referenceTransactionHookStream, os.Stdin)

	if returnCode, err := stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return referenceTransactionHookStream.Recv()
	}, f, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("error when receiving data for reference-transaction hook: %v", err)
	} else if returnCode != 0 {
		return hookError{returnCode: int(returnCode)}
	}

	return nil
}

func packObjectsHook(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	if err := handlePackObjectsWithSidechannel(ctx, payload, hookClient, args); err != nil {
		return hookError{returnCode: 1, err: fmt.Errorf("RPC failed: %w", err)}
	}

	return nil
}

func handlePackObjectsWithSidechannel(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	ctx, wt, err := hook.SetupSidechannel(ctx, payload, func(c *net.UnixConn) error {
		return stream.ProxyPktLine(c, os.Stdin, os.Stdout, os.Stderr)
	})
	if err != nil {
		return fmt.Errorf("SetupSidechannel: %w", err)
	}
	defer wt.Close()

	var glID, glUsername, gitProtocol string

	// TODO: remove this conditional in 15.2.
	// Since the git2go binary is replaced before the gitaly binary, there
	// is a period of time during an upgrade when the gitaly binary is older
	// than the corresponding git2go binary, which means gitaly will still
	// be sending ReceiveHooksPayload until the upgrade is finished.
	if payload.UserDetails != nil {
		glID = payload.UserDetails.UserID
		glUsername = payload.UserDetails.Username
		gitProtocol = payload.UserDetails.Protocol
	} else if payload.ReceiveHooksPayload != nil {
		glID = payload.ReceiveHooksPayload.UserID
		glUsername = payload.ReceiveHooksPayload.Username
		gitProtocol = payload.ReceiveHooksPayload.Protocol
	}

	ctx = metadata.AppendToOutgoingContext(
		ctx,
		"user_id",
		glID,
		"username",
		glUsername,
		"protocol",
		gitProtocol,
	)

	if _, err := hookClient.PackObjectsHookWithSidechannel(
		ctx,
		&gitalypb.PackObjectsHookWithSidechannelRequest{
			Repository:  payload.Repo,
			Args:        args,
			GlId:        glID,
			GlUsername:  glUsername,
			GitProtocol: gitProtocol,
		},
	); err != nil {
		return fmt.Errorf("call PackObjectsHookWithSidechannel: %w", err)
	}

	return wt.Wait()
}
