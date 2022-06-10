package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/git-lfs/git-lfs/v3/lfs"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/smudge"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/labkit/log"
)

func filter(ctx context.Context, cfg smudge.Config, to io.Writer, from io.Reader) (returnedErr error) {
	client, err := gitlab.NewHTTPClient(log.ContextLogger(ctx), cfg.Gitlab, cfg.TLS, prometheus.Config{})
	if err != nil {
		return fmt.Errorf("creating HTTP client: %w", err)
	}

	output, err := smudgeOneObject(ctx, cfg, client, from)
	if err != nil {
		return fmt.Errorf("smudging contents: %w", err)
	}
	defer func() {
		if err := output.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("closing LFS object: %w", err)
		}
	}()

	if _, err := io.Copy(to, output); err != nil {
		return fmt.Errorf("writing smudged contents: %w", err)
	}

	return nil
}

// processState encodes a state machine for handling long-running filter processes.
type processState int

const (
	// processStateAnnounce is the initial state where we expect the client to announce its
	// presence.
	processStateAnnounce = processState(iota)
	// processStateVersions is the state where the client announces all its known versions.
	processStateVersions
	// processStateCapabilities is the state where we have announced our own supported version
	// to the client. The client now starts to send its supported capabilities.
	processStateCapabilities
	// processStateCommand is the state where the client sends the command it wants us to
	// perform.
	processStateCommand
	// processStateSmudgeMetadata is the state where the client sends metadata of the file
	// that should be smudged.
	processStateSmudgeMetadata
	// processStateSmudgeContent is the state where the client sends the contents of the file
	// that should be smudged.
	processStateSmudgeContent
)

func process(ctx context.Context, cfg smudge.Config, to io.Writer, from io.Reader) error {
	client, err := gitlab.NewHTTPClient(log.ContextLogger(ctx), cfg.Gitlab, cfg.TLS, prometheus.Config{})
	if err != nil {
		return fmt.Errorf("creating HTTP client: %w", err)
	}

	scanner := pktline.NewScanner(from)

	writer := bufio.NewWriter(to)

	buf := make([]byte, pktline.MaxPktSize-4)
	var content bytes.Buffer

	clientSupportsVersion2 := false
	clientSupportsSmudgeCapability := false

	state := processStateAnnounce
	for scanner.Scan() {
		line := scanner.Bytes()

		var data []byte
		if !pktline.IsFlush(line) {
			payload, err := pktline.Payload(line)
			if err != nil {
				return fmt.Errorf("getting payload: %w", err)
			}

			data = payload
		}

		switch state {
		case processStateAnnounce:
			if !bytes.Equal(data, []byte("git-filter-client\n")) {
				return fmt.Errorf("invalid client %q", string(data))
			}

			state = processStateVersions
		case processStateVersions:
			// The client will announce one or more supported versions to us. We need to
			// collect them all in order to determine whether we do in fact support one
			// of the announced versions.
			if !pktline.IsFlush(line) {
				if !bytes.HasPrefix(data, []byte("version=")) {
					return fmt.Errorf("expected version, got %q", string(data))
				}

				// We only support version two of this protocol, so we have to check
				// whether that version is announced by the client.
				if bytes.Equal(data, []byte("version=2\n")) {
					clientSupportsVersion2 = true
				}

				break
			}

			// We have gotten a flush packet, so the client is done announcing its
			// versions. If we haven't seen our version announced then it's time to
			// quit.
			if !clientSupportsVersion2 {
				return fmt.Errorf("client does not support version 2")
			}

			// Announce that we're a server and that we're talking version 2 of this
			// protocol.
			if _, err := pktline.WriteString(writer, "git-filter-server\n"); err != nil {
				return fmt.Errorf("announcing server presence: %w", err)
			}

			if _, err := pktline.WriteString(writer, "version=2\n"); err != nil {
				return fmt.Errorf("announcing server version: %w", err)
			}

			if err := pktline.WriteFlush(writer); err != nil {
				return fmt.Errorf("flushing announcement: %w", err)
			}

			state = processStateCapabilities
		case processStateCapabilities:
			// Similar as above, the client will now announce all the capabilities it
			// supports. We only support the "smudging" capability.
			if !pktline.IsFlush(line) {
				if !bytes.HasPrefix(data, []byte("capability=")) {
					return fmt.Errorf("expected capability, got: %q", string(data))
				}

				// We only support smudging contents.
				if bytes.Equal(data, []byte("capability=smudge\n")) {
					clientSupportsSmudgeCapability = true
				}

				break
			}

			// If the client doesn't support smudging then we're done.
			if !clientSupportsSmudgeCapability {
				return fmt.Errorf("client does not support smudge capability")
			}

			// Announce that the only capability we support ourselves is smudging.
			if _, err := pktline.WriteString(writer, "capability=smudge\n"); err != nil {
				return fmt.Errorf("announcing smudge capability: %w", err)
			}

			if err := pktline.WriteFlush(writer); err != nil {
				return fmt.Errorf("flushing capability announcement: %w", err)
			}

			state = processStateCommand
		case processStateCommand:
			// We're now in the processing loop where the client may announce one or
			// more smudge commands.
			if !bytes.Equal(data, []byte("command=smudge\n")) {
				return fmt.Errorf("expected smudge command, got %q", string(data))
			}

			state = processStateSmudgeMetadata
		case processStateSmudgeMetadata:
			// The client sends us various information about the blob like the path
			// name or treeish. We don't care about that information, so we just wait
			// until we get the flush packet.
			if !pktline.IsFlush(line) {
				break
			}

			content.Reset()

			state = processStateSmudgeContent
		case processStateSmudgeContent:
			// When we receive a flush packet we know that the client is done sending us
			// the clean data.
			if pktline.IsFlush(line) {
				smudgedReader, err := smudgeOneObject(ctx, cfg, client, &content)
				if err != nil {
					log.ContextLogger(ctx).WithError(err).Error("failed smudging LFS pointer")

					if _, err := pktline.WriteString(writer, "status=error\n"); err != nil {
						return fmt.Errorf("reporting failure: %w", err)
					}

					if err := pktline.WriteFlush(writer); err != nil {
						return fmt.Errorf("flushing error: %w", err)
					}

					state = processStateCommand
					break
				}
				defer smudgedReader.Close()

				if _, err := pktline.WriteString(writer, "status=success\n"); err != nil {
					return fmt.Errorf("sending status: %w", err)
				}

				if err := pktline.WriteFlush(writer); err != nil {
					return fmt.Errorf("flushing status: %w", err)
				}

				// Read the smudged results in batches and relay it to the client.
				// Because pktlines are limited in size we only ever read at most
				// that many bytes.
				var isEOF bool
				for !isEOF {
					n, err := smudgedReader.Read(buf)
					if err != nil {
						if !errors.Is(err, io.EOF) {
							return fmt.Errorf("reading smudged contents: %w", err)
						}

						isEOF = true
					}

					if n > 0 {
						if _, err := pktline.WriteString(writer, string(buf[:n])); err != nil {
							return fmt.Errorf("writing smudged contents: %w", err)
						}
					}
				}
				smudgedReader.Close()

				// We're done writing the smudged contents to the client, so we need
				// to tell the client.
				if err := pktline.WriteFlush(writer); err != nil {
					return fmt.Errorf("flushing smudged contents: %w", err)
				}

				// We now have the opportunity to correct the status in case an
				// error happened. For now we don't bother though and just abort the
				// whole process in case we failed to read an LFS object, and that's
				// why we just flush a second time.
				if err := pktline.WriteFlush(writer); err != nil {
					return fmt.Errorf("flushing final status: %w", err)
				}

				// We are now ready to accept another command.
				state = processStateCommand
				break
			}

			// Write the pktline into our buffer. Ideally, we could avoid slurping the
			// whole content into memory first. But unfortunately, this is impossible in
			// the context of long-running processes: the server-side _must not_ answer
			// to the client before it has received all contents. And in the case we got
			// a non-LFS-pointer as input, this means we have to slurp in all of its
			// contents so that we can echo it back to the caller.
			if _, err := content.Write(data); err != nil {
				return fmt.Errorf("could not write clean data: %w", err)
			}
		}

		if err := writer.Flush(); err != nil {
			return fmt.Errorf("could not flush: %w", err)
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("error scanning input: %w", err)
	}

	if state != processStateCommand {
		return fmt.Errorf("unexpected termination in state %v", state)
	}

	return nil
}

func smudgeOneObject(ctx context.Context, cfg smudge.Config, gitlabClient *gitlab.HTTPClient, from io.Reader) (io.ReadCloser, error) {
	logger := log.ContextLogger(ctx)

	ptr, contents, err := lfs.DecodeFrom(from)
	if err != nil {
		// This isn't a valid LFS pointer. Just copy the existing pointer data.
		return io.NopCloser(contents), nil
	}

	logger.WithField("oid", ptr.Oid).Debug("decoded LFS OID")

	qs := url.Values{}
	qs.Set("oid", ptr.Oid)
	qs.Set("gl_repository", cfg.GlRepository)
	u := url.URL{Path: "/lfs", RawQuery: qs.Encode()}

	response, err := gitlabClient.Get(ctx, u.String())
	if err != nil {
		return nil, fmt.Errorf("error loading LFS object: %v", err)
	}

	if response.StatusCode == 200 {
		return response.Body, nil
	}

	if err := response.Body.Close(); err != nil {
		logger.WithError(err).Error("closing LFS pointer body: %w", err)
	}

	return io.NopCloser(contents), nil
}
