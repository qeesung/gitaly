package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	internalclient "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service"
	"google.golang.org/grpc"
)

type subcmd interface {
	FlagSet() *flag.FlagSet
	Exec(flags *flag.FlagSet, config config.Config) error
}

const (
	defaultDialTimeout        = 10 * time.Second
	paramVirtualStorage       = "virtual-storage"
	paramRelativePath         = "repository"
	paramAuthoritativeStorage = "authoritative-storage"
)

var subcommands = map[string]subcmd{
	sqlPingCmdName:                &sqlPingSubcommand{},
	sqlMigrateCmdName:             newSQLMigrateSubCommand(os.Stdout),
	dialNodesCmdName:              newDialNodesSubcommand(os.Stdout),
	sqlMigrateDownCmdName:         &sqlMigrateDownSubcommand{},
	sqlMigrateStatusCmdName:       &sqlMigrateStatusSubcommand{},
	datalossCmdName:               newDatalossSubcommand(),
	acceptDatalossCmdName:         &acceptDatalossSubcommand{},
	setReplicationFactorCmdName:   newSetReplicatioFactorSubcommand(os.Stdout),
	removeRepositoryCmdName:       newRemoveRepository(logger, os.Stdout),
	trackRepositoryCmdName:        newTrackRepository(logger, os.Stdout),
	trackRepositoriesCmdName:      newTrackRepositories(logger, os.Stdout),
	listUntrackedRepositoriesName: newListUntrackedRepositories(logger, os.Stdout),
	checkCmdName:                  newCheckSubcommand(os.Stdout, service.AllChecks()...),
	metadataCmdName:               newMetadataSubcommand(os.Stdout),
	verifyCmdName:                 newVerifySubcommand(os.Stdout),
	listStoragesCmdName:           newListStorages(os.Stdout),
}

// subCommand returns an exit code, to be fed into os.Exit.
func subCommand(conf config.Config, arg0 string, argRest []string) int {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	go func() {
		<-interrupt
		os.Exit(130) // indicates program was interrupted
	}()

	subcmd, ok := subcommands[arg0]
	if !ok {
		printfErr("%s: unknown subcommand: %q\n", progname, arg0)
		return 1
	}

	flags := subcmd.FlagSet()

	if err := flags.Parse(argRest); err != nil {
		printfErr("%s\n", err)
		return 1
	}

	if err := subcmd.Exec(flags, conf); err != nil {
		printfErr("%s\n", err)
		return 1
	}

	return 0
}

func getNodeAddress(cfg config.Config) (string, error) {
	switch {
	case cfg.SocketPath != "":
		return "unix:" + cfg.SocketPath, nil
	case cfg.ListenAddr != "":
		return "tcp://" + cfg.ListenAddr, nil
	case cfg.TLSListenAddr != "":
		return "tls://" + cfg.TLSListenAddr, nil
	default:
		return "", errors.New("no Praefect address configured")
	}
}

func openDB(conf config.DB) (*sql.DB, func(), error) {
	ctx := context.Background()

	openDBCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := glsql.OpenDB(openDBCtx, conf)
	if err != nil {
		return nil, nil, fmt.Errorf("sql open: %v", err)
	}

	clean := func() {
		if err := db.Close(); err != nil {
			printfErr("sql close: %v\n", err)
		}
	}

	return db, clean, nil
}

func printfErr(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format, a...)
}

func subCmdDial(ctx context.Context, addr, token string, timeout time.Duration, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts = append(opts,
		grpc.WithBlock(),
		internalclient.UnaryInterceptor(),
		internalclient.StreamInterceptor(),
	)

	if len(token) > 0 {
		opts = append(opts,
			grpc.WithPerRPCCredentials(
				gitalyauth.RPCCredentialsV2(token),
			),
		)
	}

	return client.DialContext(ctx, addr, opts)
}

type requiredParameterError string

func (p requiredParameterError) Error() string {
	return fmt.Sprintf("%q is a required parameter", string(p))
}

type unexpectedPositionalArgsError struct{ Command string }

func (err unexpectedPositionalArgsError) Error() string {
	return fmt.Sprintf("%s doesn't accept positional arguments", err.Command)
}
