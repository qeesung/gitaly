package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v16/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/env"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/stream"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"gitlab.com/gitlab-org/labkit/correlation"
	labkitcorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	labkittracing "gitlab.com/gitlab-org/labkit/tracing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type hookError struct {
	returnCode int
	err        error
	clientMsg  string
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Since the environment is sanitized at the moment, we're only
	// using this to extract the correlation ID. The finished() call
	// to clean up the tracing will be a NOP here.
	ctx, finished := labkittracing.ExtractFromEnv(ctx)
	defer finished()

	logger, err := configureLogger(ctx)
	if err != nil {
		fmt.Printf("configuring logger failed: %v", err)
		os.Exit(1)
	}

	if err := run(ctx, os.Args); err != nil {
		var hookError hookError
		if errors.As(err, &hookError) {
			if hookError.err != nil {
				notifyError("error executing git hook")
				logger.WithError(hookError.err).Error("error executing git hook")
			}
			if hookError.clientMsg != "" {
				notifyError(hookError.clientMsg)
			}
			os.Exit(hookError.returnCode)
		}

		notifyError("error executing git hook")
		logger.WithError(err).Error("error executing git hook")
		os.Exit(1)
	}
}

// configureLogger configures the logger used by gitaly-hooks. As both stdout and stderr might be interpreted by Git, we
// need to log to a file instead. If the `log.GitalyLogDirEnvKey` environment variable is set, we thus log to a file
// contained in the directory pointed to by it, otherwise we discard any log messages.
func configureLogger(ctx context.Context) (log.Logger, error) {
	writer := io.Discard

	if logDir := os.Getenv(log.GitalyLogDirEnvKey); logDir != "" {
		logFile, err := os.OpenFile(filepath.Join(logDir, "gitaly_hooks.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm.SharedFile)
		if err != nil {
			// Ignore this error as we cannot do anything about it anyway. We cannot write anything to
			// stdout or stderr as that might break hooks, and we have no other destination to log to.
		} else {
			writer = logFile
		}
	}

	logger, err := log.Configure(writer, "text", "info")
	if err != nil {
		return nil, err
	}

	return logger.WithField(correlation.FieldName, correlation.ExtractFromContext(ctx)), nil
}

// Both stderr and stdout of gitaly-hooks are streamed back to clients. stdout is processed by client
// git process transparently. stderr is dumped directly to client's stdout. Thus, we must be cautious
// what to write into stderr.
func notifyError(msg string) {
	fmt.Fprintln(os.Stderr, msg)
}

func run(ctx context.Context, args []string) error {
	switch filepath.Base(args[0]) {
	case "gitaly-hooks":
		if len(args) < 2 {
			return fmt.Errorf("requires hook name. args: %v", args)
		}

		subCmd := args[1]

		switch subCmd {
		case "git":
			return executeHook(ctx, hookCommand{
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

		return executeHook(ctx, hookCommand, args[1:])
	}
}

func executeHook(ctx context.Context, cmd hookCommand, args []string) error {
	payload, err := git.HooksPayloadFromEnv(os.Environ())
	if err != nil {
		return fmt.Errorf("error when getting hooks payload: %w", err)
	}

	// If the hook wasn't requested, then we simply skip executing any
	// logic.
	if !payload.IsHookRequested(cmd.hookType) {
		return nil
	}

	ctx = injectMetadataIntoOutgoingCtx(ctx, payload)

	conn, err := dialGitaly(ctx, payload)
	if err != nil {
		return fmt.Errorf("error when connecting to gitaly: %w", err)
	}
	defer conn.Close()

	hookClient := gitalypb.NewHookServiceClient(conn)

	return cmd.exec(ctx, payload, hookClient, args)
}

func injectMetadataIntoOutgoingCtx(ctx context.Context, payload git.HooksPayload) context.Context {
	if payload.UserDetails != nil {
		ctx = metadata.AppendToOutgoingContext(
			ctx,
			"user_id",
			payload.UserDetails.UserID,
			"username",
			payload.UserDetails.Username,
			"remote_ip",
			payload.UserDetails.RemoteIP,
		)
	}

	for _, flag := range payload.FeatureFlagsWithValue {
		ctx = featureflag.OutgoingCtxWithFeatureFlag(ctx, flag.Flag, flag.Enabled)
	}
	return ctx
}

func noopSender(c chan error) {}

func dialGitaly(ctx context.Context, payload git.HooksPayload) (*grpc.ClientConn, error) {
	var dialOpts []grpc.DialOption
	if payload.InternalSocketToken != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(payload.InternalSocketToken)))
	}

	// Propagate correlation ID
	unaryInterceptors := []grpc.UnaryClientInterceptor{
		labkitcorrelation.UnaryClientCorrelationInterceptor(
			labkitcorrelation.WithClientName("gitaly-hooks"),
		),
	}
	streamInterceptors := []grpc.StreamClientInterceptor{
		labkitcorrelation.StreamClientCorrelationInterceptor(
			labkitcorrelation.WithClientName("gitaly-hooks"),
		),
	}

	// Setup tracing is possible
	initializeTracing()
	if spanContext, err := tracing.ExtractSpanContextFromEnv(os.Environ()); err == nil {
		unaryInterceptors = append(unaryInterceptors, tracing.UnaryPassthroughInterceptor(spanContext))
		streamInterceptors = append(streamInterceptors, tracing.StreamPassthroughInterceptor(spanContext))
	}

	dialOpts = append(dialOpts, grpc.WithChainUnaryInterceptor(unaryInterceptors...))
	dialOpts = append(dialOpts, grpc.WithChainStreamInterceptor(streamInterceptors...))
	conn, err := client.Dial(ctx, "unix://"+payload.InternalSocket, client.WithGrpcOptions(dialOpts))
	if err != nil {
		return nil, fmt.Errorf("error when dialing: %w", err)
	}

	return conn, nil
}

func initializeTracing() {
	// All stdout and stderr are captured by Gitaly process. They may be sent back to users.
	// We don't want to bother them with these redundant logs. As a result, all logs should be
	// suppressed while labkit is in initialization phase.
	//
	//nolint:forbidigo // LabKit does not allow us to supply our own logger, so we must modify the standard logger
	// instead.
	output := logrus.StandardLogger().Out
	logrus.SetOutput(io.Discard)
	defer logrus.SetOutput(output)

	// This is a sanitized environment. It does not suppose to expose any spans. Instead, it
	// transfers incoming metadata from ENV to gRPC outgoing metadata without any modification
	// or starting new span. This technique connects the parent span in parent Gitaly process
	// and the remote spans when Gitaly handle subsequent gRPC calls issued by this hook.
	// As tracing is a nice-to-have feature, it should not interrupt the main functionality
	// of Gitaly hook. As a result, errors, if any, are ignored.
	labkittracing.Initialize()
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
		return fmt.Errorf("error when starting command for update hook: %w", err)
	}

	if returnCode, err := stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return updateHookStream.Recv()
	}, noopSender, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("error when receiving data for update hook: %w", err)
	} else if returnCode != 0 {
		return hookError{returnCode: int(returnCode)}
	}

	return nil
}

func preReceiveHook(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	preReceiveHookStream, err := hookClient.PreReceiveHook(ctx)
	if err != nil {
		return fmt.Errorf("error when getting preReceiveHookStream client for: %w", err)
	}

	if err := preReceiveHookStream.Send(&gitalypb.PreReceiveHookRequest{
		Repository:           payload.Repo,
		EnvironmentVariables: os.Environ(),
		GitPushOptions:       gitPushOptions(),
	}); err != nil {
		return fmt.Errorf("error when sending request for pre-receive hook: %w", err)
	}

	f := sendFunc(streamio.NewWriter(func(p []byte) error {
		return preReceiveHookStream.Send(&gitalypb.PreReceiveHookRequest{Stdin: p})
	}), preReceiveHookStream, os.Stdin)

	if returnCode, err := stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return preReceiveHookStream.Recv()
	}, f, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("error when receiving data for pre-receive hook: %w", err)
	} else if returnCode != 0 {
		return hookError{returnCode: int(returnCode)}
	}

	return nil
}

func postReceiveHook(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	postReceiveHookStream, err := hookClient.PostReceiveHook(ctx)
	if err != nil {
		return fmt.Errorf("error when getting stream client for post-receive hook: %w", err)
	}

	if err := postReceiveHookStream.Send(&gitalypb.PostReceiveHookRequest{
		Repository:           payload.Repo,
		EnvironmentVariables: os.Environ(),
		GitPushOptions:       gitPushOptions(),
	}); err != nil {
		return fmt.Errorf("error when sending request for post-receive hook: %w", err)
	}

	f := sendFunc(streamio.NewWriter(func(p []byte) error {
		return postReceiveHookStream.Send(&gitalypb.PostReceiveHookRequest{Stdin: p})
	}), postReceiveHookStream, os.Stdin)

	if returnCode, err := stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return postReceiveHookStream.Recv()
	}, f, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("error when receiving data for post-receive hook: %w", err)
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
		return fmt.Errorf("error when getting referenceTransactionHookStream client: %w", err)
	}

	if err := referenceTransactionHookStream.Send(&gitalypb.ReferenceTransactionHookRequest{
		Repository:           payload.Repo,
		EnvironmentVariables: os.Environ(),
		State:                state,
	}); err != nil {
		return fmt.Errorf("error when sending request for reference-transaction hook: %w", err)
	}

	f := sendFunc(streamio.NewWriter(func(p []byte) error {
		return referenceTransactionHookStream.Send(&gitalypb.ReferenceTransactionHookRequest{Stdin: p})
	}), referenceTransactionHookStream, os.Stdin)

	if returnCode, err := stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return referenceTransactionHookStream.Recv()
	}, f, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("error when receiving data for reference-transaction hook: %w", err)
	} else if returnCode != 0 {
		return hookError{returnCode: int(returnCode)}
	}

	return nil
}

func packObjectsHook(ctx context.Context, payload git.HooksPayload, hookClient gitalypb.HookServiceClient, args []string) error {
	ctx, wt, err := hook.SetupSidechannel(ctx, payload, func(c *net.UnixConn) error {
		return stream.ProxyPktLine(c, os.Stdin, os.Stdout, os.Stderr)
	})
	if err != nil {
		return hookError{returnCode: 1, err: fmt.Errorf("RPC failed: SetupSidechannel: %w", err)}
	}
	defer func() {
		// We already check the error further down.
		_ = wt.Close()
	}()

	var glID, glUsername, gitProtocol, remoteIP string

	if payload.UserDetails != nil {
		glID = payload.UserDetails.UserID
		glUsername = payload.UserDetails.Username
		gitProtocol = payload.UserDetails.Protocol
		remoteIP = payload.UserDetails.RemoteIP
	}

	if _, err := hookClient.PackObjectsHookWithSidechannel(
		ctx,
		&gitalypb.PackObjectsHookWithSidechannelRequest{
			Repository:  payload.Repo,
			Args:        args,
			GlId:        glID,
			GlUsername:  glUsername,
			GitProtocol: gitProtocol,
			RemoteIp:    remoteIP,
		},
	); err != nil {
		return wrapGRPCError(err)
	}

	if err := wt.Wait(); err != nil {
		return hookError{returnCode: 1, err: fmt.Errorf("RPC failed: %w", err)}
	}

	if err := wt.Close(); err != nil {
		return hookError{returnCode: 1, err: fmt.Errorf("RPC failed: closing sidechannel: %w", err)}
	}

	return nil
}

func wrapGRPCError(err error) error {
	wrappedErr := hookError{
		returnCode: 1,
		err:        fmt.Errorf("RPC failed: %w", err),
	}
	if e, ok := status.FromError(err); ok {
		switch e.Code() {
		case codes.ResourceExhausted:
			wrappedErr.clientMsg = "error resource exhausted, please try again later"
		}
	}
	return wrappedErr
}
