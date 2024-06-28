package hook

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
)

type procReceiveHandler struct {
	stdout        io.Writer
	stderr        io.Writer
	doneCh        chan<- error
	updates       []ReferenceUpdate
	transactionID storage.TransactionID
	atomic        bool
	pushOptions   []string
}

// NewProcReceiveHandler returns a ProcReceiveHandler implementation.
// The function, returns the handler along with a channel which indicates completion
// of the handlers usage.
//
// ProcReceiveHandler is used to intercept git-receive-pack(1)'s execute-commands
// code. This allows us to intercept reference updates before writing to the
// disk via the proc-receive hook (https://git-scm.com/docs/githooks#proc-receive).
//
// The handler is transmitted to RPCs which executed git-receive-pack(1), so they
// can accept or reject individual reference updates.
func NewProcReceiveHandler(env []string, stdin io.Reader, stdout, stderr io.Writer) (ProcReceiveHandler, <-chan error, error) {
	payload, err := git.HooksPayloadFromEnv(env)
	if err != nil {
		return nil, nil, fmt.Errorf("extracting hooks payload: %w", err)
	}

	// This hook only works when there is a transaction present.
	if payload.TransactionID == 0 {
		return nil, nil, fmt.Errorf("no transaction found in payload")
	}

	scanner := pktline.NewScanner(stdin)

	// Version and feature negotiation.
	if !scanner.Scan() {
		return nil, nil, fmt.Errorf("expected version negotiation: %w", scanner.Err())
	}

	data, err := pktline.Payload(scanner.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("receiving header: %w", err)
	}

	version, features, _ := bytes.Cut(data, []byte{0})
	if !bytes.HasPrefix(version, []byte("version=1")) {
		return nil, nil, fmt.Errorf("unsupported version: %s", data)
	}

	if !scanner.Scan() {
		return nil, nil, fmt.Errorf("expected flush: %w", scanner.Err())
	}

	if !pktline.IsFlush(scanner.Bytes()) {
		return nil, nil, fmt.Errorf("expected pkt flush")
	}

	featureRequests := parseFeatureRequest(features)
	if _, err := pktline.WriteString(stdout, fmt.Sprintf("version=1%s", featureRequests)); err != nil {
		return nil, nil, fmt.Errorf("writing version: %w", err)
	}

	if err := pktline.WriteFlush(stdout); err != nil {
		return nil, nil, fmt.Errorf("flushing version: %w", err)
	}

	var updates []ReferenceUpdate
	for scanner.Scan() {
		line := scanner.Bytes()

		// When all reference updates are transmitted, we expect a flush.
		if pktline.IsFlush(line) {
			break
		}

		data, err := pktline.Payload(line)
		if err != nil {
			return nil, nil, fmt.Errorf("receiving reference update: %w", err)
		}

		update, err := parseRefUpdate(data)
		if err != nil {
			return nil, nil, fmt.Errorf("parse reference update: %w", err)
		}
		updates = append(updates, update)
	}

	var pushOptions []string
	if featureRequests.pushOptions {
		for scanner.Scan() {
			line := scanner.Bytes()

			// When all push options are transmitted, we expect a flush.
			if pktline.IsFlush(line) {
				break
			}

			pushOption, err := pktline.Payload(line)
			if err != nil {
				return nil, nil, fmt.Errorf("getting push option payload: %w", err)
			}

			pushOptions = append(pushOptions, string(pushOption))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("parsing stdin: %w", err)
	}

	ch := make(chan error, 1)

	return &procReceiveHandler{
		transactionID: payload.TransactionID,
		atomic:        featureRequests.atomic,
		stdout:        stdout,
		stderr:        stderr,
		updates:       updates,
		doneCh:        ch,
		pushOptions:   pushOptions,
	}, ch, nil
}

// TransactionID provides the storage.TransactionID associated with the
// handler.
func (h *procReceiveHandler) TransactionID() storage.TransactionID {
	return h.transactionID
}

// Atomic denotes whether the push was atomic.
func (h *procReceiveHandler) Atomic() bool {
	return h.atomic
}

// PushOptions provides the set of push options provided to the proc-receive hook.
func (h *procReceiveHandler) PushOptions() []string {
	return h.pushOptions
}

// ReferenceUpdates provides the reference updates to be made.
func (h *procReceiveHandler) ReferenceUpdates() []ReferenceUpdate {
	return h.updates
}

// AcceptUpdate accepts a given reference update.
func (h *procReceiveHandler) AcceptUpdate(referenceName git.ReferenceName) error {
	if _, err := pktline.WriteString(h.stdout, fmt.Sprintf("ok %s", referenceName)); err != nil {
		return fmt.Errorf("write ref %s ok: %w", referenceName, err)
	}

	return nil
}

// RejectUpdate rejects a given reference update with the given reason.
func (h *procReceiveHandler) RejectUpdate(referenceName git.ReferenceName, reason string) error {
	if _, err := pktline.WriteString(h.stdout, fmt.Sprintf("ng %s %s", referenceName, reason)); err != nil {
		return fmt.Errorf("write ref %s ng: %w", referenceName, err)
	}

	return nil
}

// Write allows arbitrary data to be written to the proc-receive stderr. This is useful hook outputs
// that need to be returned through git-receive-pack(1) as Git handles encapsulating stderr in
// sideband packets and redirecting it to stdout.
func (h *procReceiveHandler) Write(p []byte) (int, error) {
	return h.stderr.Write(p)
}

// Close must be called to clean up the proc-receive hook. If the user
// of the handler encounters an error, it should be transferred to the
// hook too.
func (h *procReceiveHandler) Close(rpcErr error) error {
	defer func() {
		h.doneCh <- rpcErr
	}()

	// When we have an error, there is no need to flush.
	if rpcErr != nil {
		return nil
	}

	if err := pktline.WriteFlush(h.stdout); err != nil {
		return fmt.Errorf("flushing updates: %w", err)
	}

	return nil
}

func parseRefUpdate(data []byte) (ReferenceUpdate, error) {
	var update ReferenceUpdate

	split := bytes.Split(data, []byte(" "))
	if len(split) != 3 {
		return update, fmt.Errorf("unknown ref update format: %s", split)
	}

	update.Ref = git.ReferenceName(split[2])
	update.OldOID = git.ObjectID(split[0])
	update.NewOID = git.ObjectID(split[1])

	return update, nil
}

type procReceiveFeatureRequests struct {
	atomic      bool
	pushOptions bool
}

func (r *procReceiveFeatureRequests) String() string {
	var features []string
	if r.pushOptions {
		features = append(features, "push-options")
	}

	if r.atomic {
		features = append(features, "atomic")
	}

	if len(features) == 0 {
		return ""
	}

	return "\000" + strings.Join(features, " ")
}

// parseFeatureRequest parses the features requested.
func parseFeatureRequest(data []byte) *procReceiveFeatureRequests {
	var featureRequests procReceiveFeatureRequests

	for _, feature := range bytes.Split(bytes.TrimSpace(data), []byte(" ")) {
		switch {
		case bytes.Equal(feature, []byte("push-options")):
			featureRequests.pushOptions = true
		case bytes.Equal(feature, []byte("atomic")):
			featureRequests.atomic = true
		}
	}

	return &featureRequests
}
