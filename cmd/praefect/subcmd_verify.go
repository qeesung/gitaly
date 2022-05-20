package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

const verifyCmdName = "verify"

type verifySubcommand struct {
	stdout         io.Writer
	repositoryID   int64
	virtualStorage string
	storage        string
}

func newVerifySubcommand(stdout io.Writer) *verifySubcommand {
	return &verifySubcommand{stdout: stdout}
}

func (cmd *verifySubcommand) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(metadataCmdName, flag.ContinueOnError)
	fs.Int64Var(&cmd.repositoryID, "repository-id", 0, "the id of the repository to verify")
	fs.StringVar(&cmd.virtualStorage, "virtual-storage", "", "the virtual storage to verify")
	fs.StringVar(&cmd.storage, "storage", "", "the storage to verify")
	return fs
}

func (cmd *verifySubcommand) Exec(flags *flag.FlagSet, cfg config.Config) error {
	if flags.NArg() > 0 {
		return unexpectedPositionalArgsError{Command: flags.Name()}
	}

	var request gitalypb.MarkUnverifiedRequest
	switch {
	case cmd.repositoryID != 0:
		if cmd.virtualStorage != "" || cmd.storage != "" {
			return errors.New("virtual storage and storage can't be provided with a repository ID")
		}

		request.Selector = &gitalypb.MarkUnverifiedRequest_RepositoryId{RepositoryId: cmd.repositoryID}
	case cmd.storage != "":
		if cmd.virtualStorage == "" {
			return errors.New("virtual storage must be passed with storage")
		}

		request.Selector = &gitalypb.MarkUnverifiedRequest_Storage_{
			Storage: &gitalypb.MarkUnverifiedRequest_Storage{
				VirtualStorage: cmd.virtualStorage,
				Storage:        cmd.storage,
			},
		}
	case cmd.virtualStorage != "":
		request.Selector = &gitalypb.MarkUnverifiedRequest_VirtualStorage{VirtualStorage: cmd.virtualStorage}
	default:
		return errors.New("(repository id), (virtual storage) or (virtual storage, storage) required")
	}

	nodeAddr, err := getNodeAddress(cfg)
	if err != nil {
		return fmt.Errorf("get node address: %w", err)
	}

	ctx := context.TODO()
	conn, err := subCmdDial(ctx, nodeAddr, cfg.Auth.Token, defaultDialTimeout)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	response, err := gitalypb.NewPraefectInfoServiceClient(conn).MarkUnverified(ctx, &request)
	if err != nil {
		return fmt.Errorf("verify replicas: %w", err)
	}

	fmt.Fprintf(cmd.stdout, "%d replicas marked unverified\n", response.GetReplicasMarked())

	return nil
}
