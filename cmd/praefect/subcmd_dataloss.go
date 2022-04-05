package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

const datalossCmdName = "dataloss"

var errDatalossFlagsConflict = errors.New("fully-unavailable flag and partially-unavailable flag " +
	"cannot be used together")

type datalossSubcommand struct {
	output                      io.Writer
	virtualStorage              string
	onlyIncludeFullyUnAvailable bool
	/*
	  includePartiallyAvailable is just a variable for holding a deprecated partially-unavailable
	  flag's parsed value.
	  But we don't need this variable.
	*/
	includePartiallyAvailable bool
}

func newDatalossSubcommand() *datalossSubcommand {
	return &datalossSubcommand{output: os.Stdout}
}

func (cmd *datalossSubcommand) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(datalossCmdName, flag.ContinueOnError)
	fs.Usage = func() {
		printfErr("Description:\n" +
			"	This command checks whether all repositories in specified virtual storage are available or not.\n" +
			"	The default behavior is to show all partially unavailable repositories.\n" +
			"	Partially unavailable repositories are ones where some replicas are unavailable." +
			" These repositories are available but not fully replicated.")
		fs.PrintDefaults()
	}
	fs.StringVar(&cmd.virtualStorage, "virtual-storage", "", "virtual storage to check for data loss")
	fs.BoolVar(&cmd.onlyIncludeFullyUnAvailable, "fully-unavailable", false, strings.TrimSpace(`
Only show repositories whose primaries are unavailable.`))
	fs.BoolVar(&cmd.includePartiallyAvailable, "partially-unavailable", false, strings.TrimSpace(`
(Deprecated, this is the default behavior of the command)
Additionally include repositories which are available but some assigned replicas
are unavailable. Such repositories are available but are not fully replicated. This
increases the chance of data loss on primary failure`))
	return fs
}

func (cmd *datalossSubcommand) println(indent int, msg string, args ...interface{}) {
	fmt.Fprint(cmd.output, strings.Repeat("  ", indent))
	fmt.Fprintf(cmd.output, msg, args...)
	fmt.Fprint(cmd.output, "\n")
}

func (cmd *datalossSubcommand) Exec(flags *flag.FlagSet, cfg config.Config) error {
	if flags.NArg() > 0 {
		return unexpectedPositionalArgsError{Command: flags.Name()}
	}
	if cmd.onlyIncludeFullyUnAvailable && cmd.includePartiallyAvailable {
		return errDatalossFlagsConflict
	}
	includePartiallyAvailable := !cmd.onlyIncludeFullyUnAvailable

	virtualStorages := []string{cmd.virtualStorage}
	if cmd.virtualStorage == "" {
		virtualStorages = make([]string, len(cfg.VirtualStorages))
		for i := range cfg.VirtualStorages {
			virtualStorages[i] = cfg.VirtualStorages[i].Name
		}
	}
	sort.Strings(virtualStorages)

	nodeAddr, err := getNodeAddress(cfg)
	if err != nil {
		return err
	}

	ctx := context.Background()
	conn, err := subCmdDial(ctx, nodeAddr, cfg.Auth.Token, defaultDialTimeout)
	if err != nil {
		return fmt.Errorf("error dialing: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("error closing connection: %v", err)
		}
	}()

	client := gitalypb.NewPraefectInfoServiceClient(conn)

	for _, vs := range virtualStorages {
		resp, err := client.DatalossCheck(ctx, &gitalypb.DatalossCheckRequest{
			VirtualStorage:             vs,
			IncludePartiallyReplicated: includePartiallyAvailable,
		})
		if err != nil {
			return fmt.Errorf("error checking: %v", err)
		}

		cmd.println(0, "Virtual storage: %s", vs)
		if len(resp.Repositories) == 0 {
			msg := "All repositories are available!"
			if includePartiallyAvailable {
				msg = "All repositories are fully available on all assigned storages!"
			}

			cmd.println(1, msg)
			continue
		}

		cmd.println(1, "Repositories:")
		for _, repo := range resp.Repositories {
			unavailable := ""
			if repo.Unavailable {
				unavailable = " (unavailable)"
			}

			cmd.println(2, "%s%s:", repo.RelativePath, unavailable)

			primary := repo.Primary
			if primary == "" {
				primary = "No Primary"
			}
			cmd.println(3, "Primary: %s", primary)

			cmd.println(3, "In-Sync Storages:")
			for _, storage := range repo.Storages {
				if storage.BehindBy != 0 {
					continue
				}

				cmd.println(4, "%s%s%s",
					storage.Name,
					assignedMessage(storage.Assigned),
					unhealthyMessage(storage.Healthy),
				)
			}

			cmd.println(3, "Outdated Storages:")
			for _, storage := range repo.Storages {
				if storage.BehindBy == 0 {
					continue
				}

				plural := ""
				if storage.BehindBy > 1 {
					plural = "s"
				}

				cmd.println(4, "%s is behind by %d change%s or less%s%s",
					storage.Name,
					storage.BehindBy,
					plural,
					assignedMessage(storage.Assigned),
					unhealthyMessage(storage.Healthy),
				)
			}
		}
	}

	return nil
}

func unhealthyMessage(healthy bool) string {
	if healthy {
		return ""
	}

	return ", unhealthy"
}

func assignedMessage(assigned bool) string {
	assignedMsg := ""
	if assigned {
		assignedMsg = ", assigned host"
	}

	return assignedMsg
}
