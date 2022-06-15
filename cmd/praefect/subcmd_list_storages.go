package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/olekukonko/tablewriter"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
)

const (
	listStoragesCmdName = "list-storages"
)

type listStorages struct {
	virtualStorage string
	w              io.Writer
}

func newListStorages(w io.Writer) *listStorages {
	return &listStorages{w: w}
}

func (cmd *listStorages) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(listStoragesCmdName, flag.ExitOnError)
	fs.StringVar(
		&cmd.virtualStorage,
		paramVirtualStorage,
		"",
		"name of the virtual storage to list storages for",
	)
	fs.Usage = func() {
		printfErr("Description:\n" +
			"	This command lists virtual storages and their associated storages.\n" +
			"	Passing a virtual-storage argument will print out the storage associated with\n" +
			"       that particular virtual storage.\n")
		fs.PrintDefaults()
	}

	return fs
}

func (cmd listStorages) Exec(flags *flag.FlagSet, cfg config.Config) error {
	if flags.NArg() > 0 {
		return unexpectedPositionalArgsError{Command: flags.Name()}
	}

	table := tablewriter.NewWriter(cmd.w)
	table.SetHeader([]string{"VIRTUAL_STORAGE", "NODE", "ADDRESS"})
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAutoFormatHeaders(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("\t") // pad with tabs
	table.SetNoWhiteSpace(true)

	if cmd.virtualStorage != "" {
		for _, virtualStorage := range cfg.VirtualStorages {
			if virtualStorage.Name != cmd.virtualStorage {
				continue
			}

			for _, node := range virtualStorage.Nodes {
				table.Append([]string{virtualStorage.Name, node.Storage, node.Address})
			}

			table.Render()

			return nil
		}

		fmt.Fprintf(cmd.w, "No virtual storages named %s.\n", cmd.virtualStorage)

		return nil
	}

	for _, virtualStorage := range cfg.VirtualStorages {
		for _, node := range virtualStorage.Nodes {
			table.Append([]string{virtualStorage.Name, node.Storage, node.Address})
		}
	}

	table.Render()

	return nil
}
