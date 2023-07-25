package praefect

import (
	"fmt"

	"github.com/olekukonko/tablewriter"
	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

func newListStoragesCommand() *cli.Command {
	return &cli.Command{
		Name:  "list-storages",
		Usage: "list virtual storages and their associated Gitaly nodes",
		Description: `List virtual storages and Gitaly nodes associated with the virtual storages.

Returns a table with the following columns:

- Virtual storage: Name of the virtual storage Praefect provides to clients.
- Gitaly node: Name of a Gitaly node that manages storage for the virtual storage.
- Gitaly node address: Address of the Gitaly node that manages storage for the virtual storage.

If the virtual-storage flag:

- Is specified, list only Gitaly nodes for a particular virtual storage.
- Is not specified, list Gitaly nodes for all virtual storages.

Example: praefect --config praefect.config.toml list-storages --virtual-storage default`,
		HideHelpCommand: true,
		Action:          listStoragesAction,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  paramVirtualStorage,
				Usage: "name of the virtual storage to list storages for",
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

func listStoragesAction(ctx *cli.Context) error {
	logger := log.Default()
	conf, err := getConfig(logger, ctx.String(configFlagName))
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(ctx.App.Writer)
	table.SetHeader([]string{"Virtual storage", "Gitaly node", "Gitaly node address"})
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAutoFormatHeaders(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
	table.SetCenterSeparator("|")

	if pickedVirtualStorage := ctx.String(paramVirtualStorage); pickedVirtualStorage != "" {
		for _, virtualStorage := range conf.VirtualStorages {
			if virtualStorage.Name != pickedVirtualStorage {
				continue
			}

			for _, node := range virtualStorage.Nodes {
				table.Append([]string{virtualStorage.Name, node.Storage, node.Address})
			}

			table.Render()

			return nil
		}

		fmt.Fprintf(ctx.App.Writer, "No virtual storages named %s.\n", pickedVirtualStorage)

		return nil
	}

	for _, virtualStorage := range conf.VirtualStorages {
		for _, node := range virtualStorage.Nodes {
			table.Append([]string{virtualStorage.Name, node.Storage, node.Address})
		}
	}

	table.Render()

	return nil
}
