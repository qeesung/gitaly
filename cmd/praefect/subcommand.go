package main

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
)

// subCommand returns an exit code, to be fed into os.Exit.
func subCommand(conf config.Config, arg0 string, argRest []string) int {
	switch arg0 {
	case "sql-ping":
		return sqlPing(conf)
	case "sql-migrate":
		return sqlMigrate(conf)
	default:
		fmt.Printf("%s: unknown subcommand: %q\n", progname, arg0)
		return 1
	}
}

func sqlPing(conf config.Config) int {
	const subCmd = progname + " sql-ping"

	if err := datastore.CheckPostgresVersion(conf); err != nil {
		fmt.Printf("%s: fail: %v\n", subCmd, err)
		return 1
	}

	fmt.Printf("%s: OK\n", subCmd)
	return 0
}

func sqlMigrate(conf config.Config) int {
	const subCmd = progname + " sql-migrate"

	n, err := datastore.Migrate(conf)
	if err != nil {
		fmt.Printf("%s: fail: %v\n", subCmd, err)
		return 1
	}

	fmt.Printf("%s: OK (applied %d migrations)\n", subCmd, n)
	return 0
}
