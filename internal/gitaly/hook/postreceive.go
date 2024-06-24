package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const (
	// A standard terminal window is (at least) 80 characters wide.
	terminalWidth                = 80
	gitRemoteMessagePrefixLength = len("remote: ")
	terminalMessagePadding       = 2

	// Git prefixes remote messages with "remote: ", so this width is subtracted
	// from the width available to us.
	maxMessageWidth = terminalWidth - gitRemoteMessagePrefixLength

	// Our centered text shouldn't start or end right at the edge of the window,
	// so we add some horizontal padding: 2 chars on either side.
	maxMessageTextWidth = maxMessageWidth - 2*terminalMessagePadding
)

func printMessages(messages []gitlab.PostReceiveMessage, w io.Writer) error {
	for _, message := range messages {
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}

		switch message.Type {
		case "basic":
			if _, err := w.Write([]byte(message.Message)); err != nil {
				return err
			}
		case "alert":
			if err := printAlert(message, w); err != nil {
				return err
			}
		default:
			return fmt.Errorf("invalid message type: %v", message.Type)
		}

		if _, err := w.Write([]byte("\n\n")); err != nil {
			return err
		}
	}

	return nil
}

func centerLine(b []byte) []byte {
	b = bytes.TrimSpace(b)
	linePadding := int(math.Max((float64(maxMessageWidth)-float64(len(b)))/2, 0))
	return append(bytes.Repeat([]byte(" "), linePadding), b...)
}

func printAlert(m gitlab.PostReceiveMessage, w io.Writer) error {
	if _, err := w.Write(bytes.Repeat([]byte("="), maxMessageWidth)); err != nil {
		return err
	}

	if _, err := w.Write([]byte("\n\n")); err != nil {
		return err
	}

	words := strings.Fields(m.Message)

	line := bytes.NewBufferString("")

	for _, word := range words {
		if line.Len()+1+len(word) > maxMessageTextWidth {
			if _, err := w.Write(append(centerLine(line.Bytes()), '\n')); err != nil {
				return err
			}
			line.Reset()
		}

		if _, err := line.WriteString(word + " "); err != nil {
			return err
		}
	}

	if _, err := w.Write(centerLine(line.Bytes())); err != nil {
		return err
	}

	if _, err := w.Write([]byte("\n\n")); err != nil {
		return err
	}

	if _, err := w.Write(bytes.Repeat([]byte("="), maxMessageWidth)); err != nil {
		return err
	}

	return nil
}

//nolint:revive // This is unintentionally missing documentation.
func (m *GitLabHookManager) PostReceiveHook(ctx context.Context, repo *gitalypb.Repository, pushOptions, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	payload, err := git.HooksPayloadFromEnv(env)
	if err != nil {
		return structerr.NewInternal("extracting hooks payload: %w", err)
	}

	// When transactions are enabled, new writes to the repository are only available for readers (other than the
	// transaction itself) after the transaction is committed. Commit usually happens in middleware after the gRPC
	// request handler returns. Because the post-receive hook is invoked within the request handler, the transaction
	// will still be in flight, and the new writes will not be available to other transactions yet. Rails sends
	// further requests from its post-receive handler. Each of these requests are handled in their own transactions.
	//
	// To address this, explicitly commit the transaction here. We then begin a new transaction to provide the hook
	// with read-only access to the repository and its newly written changes. A new transaction is needed because
	// the previous transaction's snapshot would've been discarded by the commit. As the changes have been committed,
	// the subsequent requests sent from Rails' post-receive handler will have access to the new changes.
	//
	// We perform this dance here instead of the transaction middleware as the post-receive hook may need to write
	// messages directly to the client.
	if payload.TransactionID > 0 {
		tx, err := m.txRegistry.Get(payload.TransactionID)
		if err != nil {
			return fmt.Errorf("get transaction: %w", err)
		}

		originalRepo := tx.OriginalRepository(repo)

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit transaction: %w", err)
		}

		tx, err = m.partitionManager.Begin(ctx, originalRepo.GetStorageName(), originalRepo.GetRelativePath(), 0, storagemgr.TransactionOptions{
			ReadOnly: true,
		})
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer func() {
			if err := tx.Commit(ctx); err != nil {
				m.logger.WithError(err).Error("failed committing post-receive transaction")
			}
		}()

		repo = tx.RewriteRepository(originalRepo)
	}

	changes, err := io.ReadAll(stdin)
	if err != nil {
		return structerr.NewInternal("reading stdin from request: %w", err)
	}

	if isPrimary(payload) {
		if err := m.postReceiveHook(ctx, payload, repo, pushOptions, env, changes, stdout, stderr); err != nil {
			m.logger.WithError(err).WarnContext(ctx, "stopping transaction because post-receive hook failed")

			// If the post-receive hook declines the push, then we need to stop any
			// secondaries voting on the transaction.
			if err := m.stopTransaction(ctx, payload); err != nil {
				m.logger.WithError(err).ErrorContext(ctx, "failed stopping transaction in post-receive hook")
			}

			return err
		}
	}

	if err := m.synchronizeHookExecution(ctx, payload, "post-receive"); err != nil {
		return fmt.Errorf("synchronizing post-receive hook: %w", err)
	}

	return nil
}

func (m *GitLabHookManager) postReceiveHook(ctx context.Context, payload git.HooksPayload, repo *gitalypb.Repository, pushOptions, env []string, stdin []byte, stdout, stderr io.Writer) error {
	if len(stdin) == 0 {
		return structerr.NewInternal("hook got no reference updates")
	}

	if payload.UserDetails == nil {
		return structerr.NewInternal("payload has no receive hooks info")
	}
	if payload.UserDetails.UserID == "" {
		return structerr.NewInternal("user ID not set")
	}
	if repo.GetGlRepository() == "" {
		return structerr.NewInternal("repository not set")
	}

	ok, messages, err := m.gitlabClient.PostReceive(
		ctx, repo.GetGlRepository(),
		payload.UserDetails.UserID,
		string(stdin),
		pushOptions...,
	)
	if err != nil {
		return fmt.Errorf("GitLab: %w", err)
	}

	if err := printMessages(messages, stdout); err != nil {
		return fmt.Errorf("error writing messages to stream: %w", err)
	}

	if !ok {
		return errors.New("")
	}

	executor, err := m.newCustomHooksExecutor(repo, "post-receive")
	if err != nil {
		return structerr.NewInternal("creating custom hooks executor: %w", err)
	}

	customEnv, err := m.customHooksEnv(ctx, payload, pushOptions, env)
	if err != nil {
		return structerr.NewInternal("constructing custom hook environment: %w", err)
	}

	if err = executor(
		ctx,
		nil,
		customEnv,
		bytes.NewReader(stdin),
		stdout,
		stderr,
	); err != nil {
		return fmt.Errorf("executing custom hooks: %w", err)
	}

	return nil
}
