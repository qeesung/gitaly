package praefect

import (
	"fmt"

	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func newAcceptDatalossCommand() *cli.Command {
	return &cli.Command{
		Name:  "accept-dataloss",
		Usage: "accept potential data loss in a repository",
		Description: `Accept potential data loss in a repository, set a new authoritative Gitaly node, and enable
the repository for writing again.

The current version of the repository on the provided Gitaly node is set as the latest version. Replications to other
nodes are scheduled to make them consistent with the new authoritative Gitaly node.

Example: praefect --config praefect.config.toml accept-dataloss --virtual-storage default --repository <gitaly_relative_path> --authoritative-storage gitaly-1`,
		HideHelpCommand: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     paramVirtualStorage,
				Usage:    "name of the repository's virtual storage",
				Required: true,
			},
			&cli.StringFlag{
				Name:     paramRelativePath,
				Usage:    "Gitaly relative path of the repository to accept data loss for",
				Required: true,
			},
			&cli.StringFlag{
				Name:     paramAuthoritativeStorage,
				Usage:    "Gitaly node with the repository to consider as authoritative",
				Required: true,
			},
		},
		Action: acceptDatalossAction,
		Before: func(context *cli.Context) error {
			if context.Args().Present() {
				return unexpectedPositionalArgsError{Command: context.Command.Name}
			}
			return nil
		},
	}
}

func acceptDatalossAction(ctx *cli.Context) error {
	logger := log.Default()
	conf, err := getConfig(logger, ctx.String(configFlagName))
	if err != nil {
		return err
	}

	nodeAddr, err := getNodeAddress(conf)
	if err != nil {
		return err
	}

	conn, err := subCmdDial(ctx.Context, nodeAddr, conf.Auth.Token, defaultDialTimeout)
	if err != nil {
		return fmt.Errorf("error dialing: %w", err)
	}
	defer conn.Close()

	client := gitalypb.NewPraefectInfoServiceClient(conn)
	if _, err := client.SetAuthoritativeStorage(ctx.Context, &gitalypb.SetAuthoritativeStorageRequest{
		VirtualStorage:       ctx.String(paramVirtualStorage),
		RelativePath:         ctx.String(paramRelativePath),
		AuthoritativeStorage: ctx.String(paramAuthoritativeStorage),
	}); err != nil {
		return cli.Exit(fmt.Errorf("set authoritative storage: %w", err), 1)
	}

	return nil
}
