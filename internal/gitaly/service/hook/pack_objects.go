package hook

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/stream"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	packObjectsServedBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitaly_pack_objects_served_bytes_total",
		Help: "Number of bytes of git-pack-objects data served to clients",
	})
	packObjectsCacheLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gitaly_pack_objects_cache_lookups_total",
		Help: "Number of lookups in the PackObjectsHook cache, divided by hit/miss",
	}, []string{"result"})
	packObjectsGeneratedBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitaly_pack_objects_generated_bytes_total",
		Help: "Number of bytes generated in PackObjectsHook by running git-pack-objects",
	})
)

func (s *server) packObjectsHook(ctx context.Context, req *gitalypb.PackObjectsHookWithSidechannelRequest, args *packObjectsArgs, stdinReader io.Reader, output io.Writer) error {
	cacheKey, stdin, err := s.computeCacheKey(ctx, req, stdinReader)
	if err != nil {
		return err
	}

	// We do not know yet who has to close stdin. In case of a cache hit, it
	// is us. In case of a cache miss, a separate goroutine will run
	// git-pack-objects, and that goroutine may outlive the current request.
	// In that case, that separate goroutine will be responsible for closing
	// stdin.
	closeStdin := true
	defer func() {
		if closeStdin {
			stdin.Close()
		}
	}()

	servedBytes, created, err := s.packObjectsCache.Fetch(ctx, cacheKey, output, func(w io.Writer) error {
		ipAddr := net.ParseIP(req.GetRemoteIp())
		if ipAddr == nil {
			// Best effort, maybe the remote IP includes source port
			if ip, _, err := net.SplitHostPort(req.GetRemoteIp()); err == nil {
				ipAddr = net.ParseIP(ip)
			}
		}
		// Ignore loop-back IPs
		if ipAddr != nil && !ipAddr.IsLoopback() {
			return s.runPackObjectsLimited(
				ctx,
				w,
				ipAddr.String(),
				req,
				args,
				stdin,
				cacheKey,
			)
		}

		return s.runPackObjects(ctx, w, req, args, stdin, cacheKey)
	})
	if err != nil {
		return err
	}

	if created {
		closeStdin = false
		packObjectsCacheLookups.WithLabelValues("miss").Inc()
	} else {
		packObjectsCacheLookups.WithLabelValues("hit").Inc()
	}

	customFields := log.CustomFieldsFromContext(ctx)
	if customFields != nil {
		customFields.RecordMetadata("pack_objects_cache.key", cacheKey)
		customFields.RecordSum("pack_objects_cache.served_bytes", int(servedBytes))
		if created {
			customFields.RecordMetadata("pack_objects_cache.hit", "false")
		} else {
			customFields.RecordMetadata("pack_objects_cache.hit", "true")
		}
	}
	packObjectsServedBytes.Add(float64(servedBytes))

	return nil
}

// computeCacheKey returns the cache key used for caching pack-objects. A cache key is made up of
// both the requested objects and essential parameters that could impact the content of the
// generated packfile. Including any insignificant information could result in a lower cache hit rate.
func (s *server) computeCacheKey(ctx context.Context, req *gitalypb.PackObjectsHookWithSidechannelRequest, stdinReader io.Reader) (string, io.ReadCloser, error) {
	cacheHash := sha256.New()

	repository := req.Repository
	storagectx.RunWithTransaction(ctx, func(tx storagectx.Transaction) {
		// The cache uses the requests as the keys. As the request's repository in the RPC handler has been rewritten
		// to point to the transaction's repository, the handler sees each request as different even if they point to
		// the same repository. Restore the original request to ensure identical requests get the same key.
		repository = tx.OriginalRepository(req.Repository)
	})

	cacheKeyPrefix, err := protojson.Marshal(&gitalypb.PackObjectsHookWithSidechannelRequest{
		Repository:  repository,
		Args:        req.Args,
		GitProtocol: req.GitProtocol,
	})
	if err != nil {
		return "", nil, err
	}
	if _, err := cacheHash.Write(cacheKeyPrefix); err != nil {
		return "", nil, err
	}
	stdin, err := bufferStdin(stdinReader, cacheHash)
	if err != nil {
		return "", nil, err
	}
	cacheKey := hex.EncodeToString(cacheHash.Sum(nil))
	return cacheKey, stdin, nil
}

func (s *server) runPackObjects(
	ctx context.Context,
	w io.Writer,
	req *gitalypb.PackObjectsHookWithSidechannelRequest,
	args *packObjectsArgs,
	stdin io.ReadCloser,
	key string,
) error {
	// We want to keep the context for logging, but we want to block all its
	// cancellation signals (deadline, cancel etc.). This is because of
	// the following scenario. Imagine client1 calls PackObjectsHook and
	// causes runPackObjects to run in a goroutine. Now suppose that client2
	// calls PackObjectsHook with the same arguments and stdin, so it joins
	// client1 in waiting for this goroutine. Now client1 hangs up before the
	// runPackObjects goroutine is done.
	//
	// If the cancellation of client1 propagated into the runPackObjects
	// goroutine this would affect client2. We don't want that. So to prevent
	// that, we suppress the cancellation of the originating context.
	ctx = helper.SuppressCancellation(ctx)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer stdin.Close()

	return s.runPackObjectsFn(ctx, s.gitCmdFactory, w, req, args, stdin, key)
}

func (s *server) runPackObjectsLimited(
	ctx context.Context,
	w io.Writer,
	limitkey string,
	req *gitalypb.PackObjectsHookWithSidechannelRequest,
	args *packObjectsArgs,
	stdin io.ReadCloser,
	key string,
) error {
	ctx = helper.SuppressCancellation(ctx)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer stdin.Close()

	if _, err := s.packObjectsLimiter.Limit(
		ctx,
		limitkey,
		func() (interface{}, error) {
			return nil,
				s.runPackObjectsFn(
					ctx,
					s.gitCmdFactory,
					w,
					req,
					args,
					stdin,
					key,
				)
		},
	); err != nil {
		return err
	}

	return nil
}

func runPackObjects(
	ctx context.Context,
	gitCmdFactory git.CommandFactory,
	w io.Writer,
	req *gitalypb.PackObjectsHookWithSidechannelRequest,
	args *packObjectsArgs,
	stdin io.Reader,
	key string,
) error {
	repo := req.GetRepository()

	counter := &helper.CountingWriter{W: w}
	sw := pktline.NewSidebandWriter(counter)
	stdout := bufio.NewWriterSize(sw.Writer(stream.BandStdout), pktline.MaxSidebandData)
	stderrBuf := &bytes.Buffer{}
	stderr := io.MultiWriter(sw.Writer(stream.BandStderr), stderrBuf)

	defer func() {
		packObjectsGeneratedBytes.Add(float64(counter.N))
		customFields := log.CustomFieldsFromContext(ctx)
		if customFields != nil {
			customFields.RecordMetadata("pack_objects_cache.key", key)
			customFields.RecordSum("pack_objects_cache.generated_bytes", int(counter.N))
			if total := totalMessage(stderrBuf.Bytes()); total != "" {
				customFields.RecordMetadata("pack_objects.compression_statistics", total)
			}
		}
	}()

	cmd, err := gitCmdFactory.New(ctx, repo, args.subcmd(),
		git.WithStdin(stdin),
		git.WithStdout(stdout),
		git.WithStderr(stderr),
		git.WithGlobalOption(args.globals()...),
	)
	if err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git-pack-objects: stderr: %q err: %w", stderrBuf.String(), err)
	}
	if err := stdout.Flush(); err != nil {
		return fmt.Errorf("flush stdout: %w", err)
	}
	return nil
}

func totalMessage(stderr []byte) string {
	start := bytes.Index(stderr, []byte("Total "))
	if start < 0 {
		return ""
	}

	end := bytes.Index(stderr[start:], []byte("\n"))
	if end < 0 {
		return ""
	}

	return string(stderr[start : start+end])
}

var (
	errNoPackObjects = errors.New("missing pack-objects")
	errNonFlagArg    = errors.New("non-flag argument")
	errNoStdout      = errors.New("missing --stdout")
)

func parsePackObjectsArgs(args []string) (*packObjectsArgs, error) {
	result := &packObjectsArgs{}

	// Check for special argument used with shallow clone:
	// https://gitlab.com/gitlab-org/git/-/blob/v2.30.0/upload-pack.c#L287-290
	if len(args) >= 2 && args[0] == "--shallow-file" && args[1] == "" {
		result.shallowFile = true
		args = args[2:]
	}

	if len(args) < 1 || args[0] != "pack-objects" {
		return nil, errNoPackObjects
	}
	args = args[1:]

	// There should always be "--stdout" somewhere. Git-pack-objects can
	// write to a file too but we don't want that in this RPC.
	// https://gitlab.com/gitlab-org/git/-/blob/v2.30.0/upload-pack.c#L296
	seenStdout := false
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return nil, errNonFlagArg
		}
		if a == "--stdout" {
			seenStdout = true
		} else {
			result.flags = append(result.flags, a)
		}
	}

	if !seenStdout {
		return nil, errNoStdout
	}

	return result, nil
}

type packObjectsArgs struct {
	shallowFile bool
	flags       []string
}

func (p *packObjectsArgs) globals() []git.GlobalOption {
	var globals []git.GlobalOption
	if p.shallowFile {
		globals = append(globals, git.ValueFlag{Name: "--shallow-file", Value: ""})
	}
	return globals
}

func (p *packObjectsArgs) subcmd() git.Command {
	sc := git.Command{
		Name:  "pack-objects",
		Flags: []git.Option{git.Flag{Name: "--stdout"}},
	}
	for _, f := range p.flags {
		sc.Flags = append(sc.Flags, git.Flag{Name: f})
	}
	return sc
}

func bufferStdin(r io.Reader, h hash.Hash) (_ io.ReadCloser, err error) {
	f, err := os.CreateTemp("", "PackObjectsHook-stdin")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()

	if err := os.Remove(f.Name()); err != nil {
		return nil, err
	}

	_, err = io.Copy(f, io.TeeReader(r, h))
	if err != nil {
		return nil, err
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	return f, nil
}

func (s *server) PackObjectsHookWithSidechannel(ctx context.Context, req *gitalypb.PackObjectsHookWithSidechannelRequest) (*gitalypb.PackObjectsHookWithSidechannelResponse, error) {
	if err := s.locator.ValidateRepository(ctx, req.GetRepository()); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	args, err := parsePackObjectsArgs(req.Args)
	if err != nil {
		return nil, structerr.NewInvalidArgument("invalid pack-objects command: %v: %w", req.Args, err)
	}

	c, err := gitalyhook.GetSidechannel(ctx)
	if err != nil {
		if errors.As(err, &gitalyhook.InvalidSidechannelAddressError{}) {
			return nil, structerr.NewInvalidArgument("%w", err)
		}
		return nil, structerr.NewInternal("get side-channel: %w", err)
	}
	defer c.Close()

	if err := s.packObjectsHook(ctx, req, args, c, c); err != nil {
		if errors.Is(err, syscall.EPIPE) {
			// EPIPE is the error we get if we try to write to c after the client has
			// closed its side of the connection. By convention, we label server side
			// errors caused by the client disconnecting with the Canceled gRPC code.
			err = structerr.NewCanceled("%w", err)
		}
		return nil, structerr.NewInternal("pack objects hook: %w", err)
	}

	if err := c.Close(); err != nil {
		return nil, structerr.NewInternal("close side-channel: %w", err)
	}

	return &gitalypb.PackObjectsHookWithSidechannelResponse{}, nil
}
