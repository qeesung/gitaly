package hook

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/internal/gitlabshell"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metadata"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"google.golang.org/grpc"
)

type hookRequest interface {
	GetEnvironmentVariables() []string
	GetRepository() *gitalypb.Repository
}

func hookRequestEnv(req hookRequest) ([]string, error) {
	gitlabshellEnv, err := gitlabshell.Env()
	if err != nil {
		return nil, err
	}
	return append(gitlabshellEnv, req.GetEnvironmentVariables()...), nil
}

func preReceiveEnv(req hookRequest) ([]string, error) {
	_, env, err := alternates.PathAndEnv(req.GetRepository())
	if err != nil {
		return nil, err
	}

	hookEnv, err := hookRequestEnv(req)
	if err != nil {
		return nil, err
	}

	return append(hookEnv, env...), nil
}

func gitlabShellHook(hookName string) string {
	return filepath.Join(config.Config.Ruby.Dir, "gitlab-shell", "hooks", hookName)
}

func (s *server) getPraefectConn(ctx context.Context, server *metadata.PraefectServer) (*grpc.ClientConn, error) {
	s.mutex.RLock()
	conn, ok := s.praefectConnPool[server.Address]
	s.mutex.RUnlock()

	if ok {
		return conn, nil
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	conn, ok = s.praefectConnPool[server.Address]
	if !ok {
		var err error
		conn, err = server.Dial(ctx)
		if err != nil {
			return nil, err
		}

		s.praefectConnPool[server.Address] = conn
	}

	return conn, nil
}

func (s *server) voteOnTransaction(stream gitalypb.HookService_PreReceiveHookServer, hash []byte, env []string) error {
	tx, err := metadata.TransactionFromEnv(env)
	if err != nil {
		if errors.Is(err, metadata.ErrTransactionNotFound) {
			// No transaction being present is valid, e.g. in case
			// there is no Praefect server or the transactions
			// feature flag is not set.
			return nil
		}
		return fmt.Errorf("could not extract transaction: %w", err)
	}

	praefectServer, err := metadata.PraefectFromEnv(env)
	if err != nil {
		return fmt.Errorf("could not extract Praefect server: %w", err)
	}

	ctx, cancel := context.WithTimeout(stream.Context(), 10*time.Second)
	defer cancel()

	praefectConn, err := s.getPraefectConn(ctx, praefectServer)
	if err != nil {
		return err
	}

	praefectClient := gitalypb.NewRefTransactionClient(praefectConn)

	response, err := praefectClient.VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{
		TransactionId:        tx.ID,
		Node:                 tx.Node,
		ReferenceUpdatesHash: hash,
	})
	if err != nil {
		return err
	}

	if response.State != gitalypb.VoteTransactionResponse_COMMIT {
		return errors.New("transaction was aborted")
	}

	return nil
}

func (s *server) PreReceiveHook(stream gitalypb.HookService_PreReceiveHookServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return helper.ErrInternal(err)
	}

	if err := validatePreReceiveHookRequest(firstRequest); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	referenceUpdatesHasher := sha1.New()
	stdin := streamio.NewReader(func() ([]byte, error) {
		req, err := stream.Recv()
		if err != nil {
			return nil, err
		}

		stdin := req.GetStdin()
		if _, err := referenceUpdatesHasher.Write(stdin); err != nil {
			return stdin, err
		}

		return stdin, nil
	})
	stdout := streamio.NewWriter(func(p []byte) error { return stream.Send(&gitalypb.PreReceiveHookResponse{Stdout: p}) })
	stderr := streamio.NewWriter(func(p []byte) error { return stream.Send(&gitalypb.PreReceiveHookResponse{Stderr: p}) })

	repoPath, err := helper.GetRepoPath(firstRequest.GetRepository())
	if err != nil {
		return helper.ErrInternal(err)
	}

	c := exec.Command(gitlabShellHook("pre-receive"))
	c.Dir = repoPath

	env, err := preReceiveEnv(firstRequest)
	if err != nil {
		return helper.ErrInternal(err)
	}

	status, err := streamCommandResponse(
		stream.Context(),
		stdin,
		stdout, stderr,
		c,
		env,
	)
	if err != nil {
		return helper.ErrInternal(err)
	}

	if err := s.voteOnTransaction(stream, referenceUpdatesHasher.Sum(nil), env); err != nil {
		return helper.ErrInternalf("error voting on transaction: %w", err)
	}

	if err := stream.SendMsg(&gitalypb.PreReceiveHookResponse{
		ExitStatus: &gitalypb.ExitStatus{Value: status},
	}); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func validatePreReceiveHookRequest(in *gitalypb.PreReceiveHookRequest) error {
	if in.GetRepository() == nil {
		return errors.New("repository is empty")
	}

	return nil
}
