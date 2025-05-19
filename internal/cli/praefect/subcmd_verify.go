package praefect

import (
	"errors"
	"fmt"

	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const verifyCmdName = "verify"

func newVerifyCommand() *cli.Command {
	return &cli.Command{
		Name:  verifyCmdName,
		Usage: "mark repository replicas as unverified",
		Description: `Mark as unverified replicas of repositories to prioritize reverification.

The subcommand sets different replicas as unverified depending on the supplied flags:

- When a repository ID is specified, all the replicas of the repository on physical storage are marked as unverified.
- When a virtual storage name is specified, all the replicas of all repositories on all physical storages associated with
  the virtual storage as marked as unverified.
- When a virtual storage name and physical storage name are specified, all the replicas of all repositories on the
  specified physical storage associated with the specified virtual storage are marked as unverified.

Reverification runs asynchronously in the background.

Examples:

- praefect --config praefect.config.toml verify --repository-id 1
- praefect --config praefect.config.toml verify --virtual-storage default
- praefect --config praefect.config.toml verify --virtual-storage default --storage <physical_storage_1>`,
		HideHelpCommand: true,
		Action:          verifyAction,
		Flags: []cli.Flag{
			&cli.Int64Flag{
				Name:  "repository-id",
				Usage: "repository ID of the repository to mark as unverified",
			},
			&cli.StringFlag{
				Name:  paramVirtualStorage,
				Usage: "name of the virtual storage with replicas to mark as unverified",
			},
			&cli.StringFlag{
				Name:  "storage",
				Usage: "name of the physical storage associated with the virtual storage with replicas to mark as unverified",
			},
		},
		Before: func(ctx *cli.Context) error {
			if ctx.Args().Present() {
				_ = cli.ShowSubcommandHelp(ctx)
				return cli.Exit(unexpectedPositionalArgsError{Command: ctx.Command.Name}, 1)
			}
			return nil
		},
	}
}

func verifyAction(appCtx *cli.Context) error {
	log.ConfigureCommand()

	conf, err := readConfig(appCtx.String(configFlagName))
	if err != nil {
		return err
	}

	repositoryID := appCtx.Int64("repository-id")
	virtualStorage := appCtx.String(paramVirtualStorage)
	storage := appCtx.String("storage")

	var request gitalypb.MarkUnverifiedRequest
	switch {
	case repositoryID != 0:
		if virtualStorage != "" || storage != "" {
			return errors.New("virtual storage and storage can't be provided with a repository ID")
		}

		request.Selector = &gitalypb.MarkUnverifiedRequest_RepositoryId{RepositoryId: repositoryID}
	case storage != "":
		if virtualStorage == "" {
			return errors.New("virtual storage must be passed with storage")
		}

		request.Selector = &gitalypb.MarkUnverifiedRequest_Storage_{
			Storage: &gitalypb.MarkUnverifiedRequest_Storage{
				VirtualStorage: virtualStorage,
				Storage:        storage,
			},
		}
	case virtualStorage != "":
		request.Selector = &gitalypb.MarkUnverifiedRequest_VirtualStorage{VirtualStorage: virtualStorage}
	default:
		return errors.New("(repository id), (virtual storage) or (virtual storage, storage) required")
	}

	nodeAddr, err := getNodeAddress(conf)
	if err != nil {
		return fmt.Errorf("get node address: %w", err)
	}

	ctx := appCtx.Context
	conn, err := subCmdDial(ctx, nodeAddr, conf.Auth.Token, defaultDialTimeout)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	response, err := gitalypb.NewPraefectInfoServiceClient(conn).MarkUnverified(ctx, &request)
	if err != nil {
		return fmt.Errorf("verify replicas: %w", err)
	}

	fmt.Fprintf(appCtx.App.Writer, "%d replicas marked unverified\n", response.GetReplicasMarked())

	return nil
}
