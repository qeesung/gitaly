package hook

import (
	"errors"
	"strings"
	"sync"

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

	stdin := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetStdin(), err
	})
	m := &sync.Mutex{}
	stdout := streamio.NewSyncWriter(m, func(p []byte) error {
		return stream.Send(&gitalypb.PackObjectsHookResponse{Stdout: p})
	})
	stderr := streamio.NewSyncWriter(m, func(p []byte) error {
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
	}

	result.flags = args
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
