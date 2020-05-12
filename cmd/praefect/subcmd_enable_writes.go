package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"

	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type UnexpectedPositionalArgsError struct{ Command string }

func (err UnexpectedPositionalArgsError) Error() string {
	return fmt.Sprintf("%s doesn't accept positional arguments", err.Command)
}

var errMissingVirtualStorage = errors.New("virtual-storage is a required parameter")

type enableWritesSubcommand struct {
	virtualStorage string
}

func (cmd *enableWritesSubcommand) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("enable-writes", flag.ContinueOnError)
	fs.StringVar(&cmd.virtualStorage, "virtual-storage", "", "name of the virtual storage to enable writes for")
	return fs
}

func (cmd *enableWritesSubcommand) Exec(flags *flag.FlagSet, cfg config.Config) error {
	if flags.NArg() > 0 {
		return UnexpectedPositionalArgsError{Command: flags.Name()}
	}

	if cmd.virtualStorage == "" {
		return errMissingVirtualStorage
	}

	nodeAddr, err := getNodeAddress(cfg)
	if err != nil {
		return err
	}

	conn, err := subCmdDial(nodeAddr, cfg.Auth.Token)
	if err != nil {
		return fmt.Errorf("error dialing: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("error closing connection: %v", err)
		}
	}()

	client := gitalypb.NewPraefectInfoServiceClient(conn)
	if _, err := client.EnableWrites(context.TODO(), &gitalypb.EnableWritesRequest{
		VirtualStorage: cmd.virtualStorage,
	}); err != nil {
		return fmt.Errorf("error enabling writes: %w", err)
	}

	return nil
}
