package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"time"

	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore/glsql"
	"google.golang.org/grpc"
)

const invocationPrefix = progname + " -config CONFIG_TOML"

// subCommand returns an exit code, to be fed into os.Exit.
func subCommand(conf config.Config, arg0 string, argRest []string) int {
	interrupt := make(chan os.Signal)
	signal.Notify(interrupt, os.Interrupt)

	go func() {
		<-interrupt
		os.Exit(130) // indicates program was interrupted
	}()

	switch arg0 {
	case "sql-ping":
		return sqlPing(conf)
	case "sql-migrate":
		return sqlMigrate(conf)
	case subCmdSQLMigrateDown:
		return sqlMigrateDown(conf, argRest)
	case "dial-nodes":
		return dialNodes(conf)
	case "reconcile":
		return reconcile(conf, argRest)
	default:
		printfErr("%s: unknown subcommand: %q\n", progname, arg0)
		return 1
	}
}

func sqlPing(conf config.Config) int {
	const subCmd = progname + " sql-ping"

	db, clean, code := openDB(conf.DB)
	if code != 0 {
		return code
	}
	defer clean()

	if err := datastore.CheckPostgresVersion(db); err != nil {
		printfErr("%s: fail: %v\n", subCmd, err)
		return 1
	}

	fmt.Printf("%s: OK\n", subCmd)
	return 0
}

func sqlMigrate(conf config.Config) int {
	const subCmd = progname + " sql-migrate"

	db, clean, code := openDB(conf.DB)
	if code != 0 {
		return code
	}
	defer clean()

	n, err := glsql.Migrate(db)
	if err != nil {
		printfErr("%s: fail: %v\n", subCmd, err)
		return 1
	}

	fmt.Printf("%s: OK (applied %d migrations)\n", subCmd, n)
	return 0
}

func openDB(conf config.DB) (*sql.DB, func(), int) {
	db, err := glsql.OpenDB(conf)
	if err != nil {
		printfErr("sql open: %v\n", err)
		return nil, nil, 1
	}

	clean := func() {
		if err := db.Close(); err != nil {
			printfErr("sql close: %v\n", err)
		}
	}

	return db, clean, 0
}

func printfErr(format string, a ...interface{}) (int, error) {
	return fmt.Fprintf(os.Stderr, format, a...)
}

func subCmdDial(addr, token string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts = append(opts,
		grpc.WithBlock(),
		grpc.WithTimeout(30*time.Second),
	)

	if len(token) > 0 {
		opts = append(opts,
			grpc.WithPerRPCCredentials(
				gitalyauth.RPCCredentialsV2(token),
			),
		)
	}

	return client.Dial(addr, opts)
}
