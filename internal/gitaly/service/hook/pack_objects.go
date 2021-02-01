package hook

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/golang/protobuf/jsonpb"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

var (
	packObjectsResponseBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gitaly_pack_objects_response_bytes_total",
		Help: "Number of bytes of git-pack-objects data returned to clients",
	}, []string{"stream"})
)

func (s *server) PackObjectsHook(stream gitalypb.HookService_PackObjectsHookServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return helper.ErrInternal(err)
	}

	if firstRequest.GetRepository() == nil {
		return helper.ErrInvalidArgument(errors.New("repository is empty"))
	}

	args, valid := parsePackObjectsArgs(firstRequest.Args)
	if !valid {
		return helper.ErrInvalidArgumentf("invalid pack-objects command: %v", firstRequest.Args)
	}

	if err := s.packObjectsHook(stream, firstRequest, args); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func (s *server) packObjectsHook(stream gitalypb.HookService_PackObjectsHookServer, firstRequest *gitalypb.PackObjectsHookRequest, args *packObjectsArgs) error {
	ctx := stream.Context()

	h := sha256.New()
	if err := (&jsonpb.Marshaler{}).Marshal(h, firstRequest); err != nil {
		return err
	}

	stdin, stdinSize, err := bufferStdin(stream, h)
	if err != nil {
		return err
	}
	defer stdin.Close()

	key := h.Sum(nil)
	var stdoutBytes, stderrBytes int64
	defer func() {
		ctxlogrus.Extract(ctx).WithFields(logrus.Fields{
			"cache_key":    fmt.Sprintf("%x", key),
			"stdin_bytes":  stdinSize,
			"stdout_bytes": atomic.LoadInt64(&stdoutBytes),
			"stderr_bytes": atomic.LoadInt64(&stderrBytes),
		}).Info("pack-objects")
	}()

	m := &sync.Mutex{}
	stdout := streamio.NewSyncWriter(m, func(p []byte) error {
		atomic.AddInt64(&stdoutBytes, int64(len(p)))
		packObjectsResponseBytes.WithLabelValues("stdout").Add(float64(len(p)))
		return stream.Send(&gitalypb.PackObjectsHookResponse{Stdout: p})
	})
	stderr := streamio.NewSyncWriter(m, func(p []byte) error {
		atomic.AddInt64(&stderrBytes, int64(len(p)))
		packObjectsResponseBytes.WithLabelValues("stderr").Add(float64(len(p)))
		return stream.Send(&gitalypb.PackObjectsHookResponse{Stderr: p})
	})

	cmd, err := git.NewCommand(ctx, firstRequest.GetRepository(), args.globals(), args.subcmd(),
		git.WithStdin(stdin),
		git.WithStdout(stdout),
		git.WithStderr(stderr),
	)
	if err != nil {
		return err
	}

	return cmd.Wait()
}

func parsePackObjectsArgs(args []string) (*packObjectsArgs, bool) {
	result := &packObjectsArgs{}

	// Check for special argument used with shallow clone:
	// https://gitlab.com/gitlab-org/git/-/blob/v2.30.0/upload-pack.c#L287-290
	if len(args) >= 2 && args[0] == "--shallow-file" && args[1] == "" {
		result.shallowFile = true
		args = args[2:]
	}

	if len(args) < 1 || args[0] != "pack-objects" {
		return nil, false
	}
	args = args[1:]

	// There should always be "--stdout" somewhere. Git-pack-objects can
	// write to a file too but we don't want that in this RPC.
	// https://gitlab.com/gitlab-org/git/-/blob/v2.30.0/upload-pack.c#L296
	seenStdout := false
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return nil, false
		}
		if a == "--stdout" {
			seenStdout = true
		} else {
			result.flags = append(result.flags, a)
		}
	}

	if !seenStdout {
		return nil, false
	}

	return result, true
}

type packObjectsArgs struct {
	shallowFile bool
	flags       []string
}

func (p *packObjectsArgs) globals() []git.GlobalOption {
	var globals []git.GlobalOption
	if p.shallowFile {
		globals = append(globals, git.ValueFlag{"--shallow-file", ""})
	}
	return globals
}

func (p *packObjectsArgs) subcmd() git.SubCmd {
	sc := git.SubCmd{
		Name:  "pack-objects",
		Flags: []git.Option{git.Flag{"--stdout"}},
	}
	for _, f := range p.flags {
		sc.Flags = append(sc.Flags, git.Flag{f})
	}
	return sc
}

func bufferStdin(stream gitalypb.HookService_PackObjectsHookServer, h hash.Hash) (r io.ReadCloser, n int64, err error) {
	defer func() {
		if r != nil && err != nil {
			r.Close()
		}
	}()

	stdin := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetStdin(), err
	})

	stdinTmp, err := ioutil.TempFile("", "PackObjectsHook")
	if err != nil {
		return nil, 0, err
	}

	if err := os.Remove(stdinTmp.Name()); err != nil {
		return nil, 0, err
	}

	n, err = io.Copy(io.MultiWriter(h, stdinTmp), stdin)
	if err != nil {
		return nil, 0, err
	}

	if _, err := stdinTmp.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}

	return stdinTmp, n, nil
}
