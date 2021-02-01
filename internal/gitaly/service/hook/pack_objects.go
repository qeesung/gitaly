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

	"github.com/golang/protobuf/jsonpb"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

func (s *server) PackObjectsHook(stream gitalypb.HookService_PackObjectsHookServer) error {
	ctx := stream.Context()

	firstRequest, err := stream.Recv()
	if err != nil {
		return helper.ErrInternal(err)
	}

	repo := firstRequest.GetRepository()
	if repo == nil {
		return helper.ErrInvalidArgument(errors.New("repository is empty"))
	}

	args, valid := parsePackObjectsArgs(firstRequest.Args)
	if err := stream.Send(&gitalypb.PackObjectsHookResponse{
		CommandAccepted: valid,
	}); err != nil {
		return helper.ErrInternal(err)
	}
	if !valid {
		return nil
	}

	h := sha256.New()
	if err := (&jsonpb.Marshaler{}).Marshal(h, firstRequest); err != nil {
		return err
	}

	stdin, err := bufferStdin(stream, h)
	if err != nil {
		return err
	}
	defer stdin.Close()

	key := h.Sum(nil)
	var outBytes int64
	logger := ctxlogrus.Extract(ctx)
	defer func() {
		logger.WithFields(logrus.Fields{
			"cache_key": fmt.Sprintf("%x", key),
			"out_bytes": outBytes,
		}).Info("pack-objects")
	}()

	m := &sync.Mutex{}
	stdout := streamio.NewSyncWriter(m, func(p []byte) error {
		outBytes += int64(len(p))
		return stream.Send(&gitalypb.PackObjectsHookResponse{Stdout: p})
	})
	stderr := streamio.NewSyncWriter(m, func(p []byte) error {
		outBytes += int64(len(p))
		return stream.Send(&gitalypb.PackObjectsHookResponse{Stderr: p})
	})

	cmd, err := git.NewCommand(ctx, repo, args.globals(), args.subcmd(),
		git.WithStdin(stdin),
		git.WithStdout(stdout),
		git.WithStderr(stderr),
	)
	if err != nil {
		return helper.ErrInternal(err)
	}

	if err := cmd.Wait(); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func parsePackObjectsArgs(args []string) (*packObjectsArgs, bool) {
	result := &packObjectsArgs{}
	if len(args) >= 2 && args[0] == "--shallow-file" && args[1] == "" {
		result.shallowFile = true
		args = args[2:]
	}

	if len(args) < 1 || args[0] != "pack-objects" {
		return nil, false
	}
	args = args[1:]

	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return nil, false
		}
		result.flags = append(result.flags, a)
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
		Name: "pack-objects",
	}
	for _, f := range p.flags {
		sc.Flags = append(sc.Flags, git.Flag{f})
	}
	return sc
}

func bufferStdin(stream gitalypb.HookService_PackObjectsHookServer, h hash.Hash) (r io.ReadCloser, err error) {
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
		return nil, err
	}

	if err := os.Remove(stdinTmp.Name()); err != nil {
		return nil, err
	}

	if _, err := io.Copy(io.MultiWriter(h, stdinTmp), stdin); err != nil {
		return nil, err
	}

	if _, err := stdinTmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	return stdinTmp, nil
}
