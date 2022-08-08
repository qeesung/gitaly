package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service"
)

const (
	checkCmdName = "check"
)

type checkSubcommand struct {
	w          io.Writer
	quiet      bool
	checkFuncs []service.CheckFunc
}

func newCheckSubcommand(writer io.Writer, checkFuncs ...service.CheckFunc) *checkSubcommand {
	return &checkSubcommand{
		w:          writer,
		checkFuncs: checkFuncs,
	}
}

func (cmd *checkSubcommand) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(checkCmdName, flag.ExitOnError)
	fs.BoolVar(&cmd.quiet, "q", false, "do not print out verbose output about each check")
	fs.Usage = func() {
		printfErr("Description:\n" +
			"	This command runs startup checks for Praefect.")
		fs.PrintDefaults()
	}

	return fs
}

var errFatalChecksFailed = errors.New("checks failed")

func (cmd *checkSubcommand) Exec(_ *flag.FlagSet, cfg config.Config) error {
	var allChecks []*service.Check
	for _, checkFunc := range cmd.checkFuncs {
		allChecks = append(allChecks, checkFunc(cfg, cmd.w, cmd.quiet))
	}

	bgContext := context.Background()
	passed := true
	var failedChecks int
	for _, check := range allChecks {
		ctx, cancel := context.WithTimeout(bgContext, 5*time.Second)
		defer cancel()

		cmd.printCheckDetails(check)

		if err := check.Run(ctx); err != nil {
			failedChecks++
			if check.Severity == service.Fatal {
				passed = false
			}
			fmt.Fprintf(cmd.w, "Failed (%s) error: %s\n", check.Severity, err.Error())
			continue
		}
		fmt.Fprintf(cmd.w, "Passed\n")
	}

	fmt.Fprintf(cmd.w, "\n")

	if !passed {
		fmt.Fprintf(cmd.w, "%d check(s) failed, at least one was fatal.\n", failedChecks)
		return errFatalChecksFailed
	}

	if failedChecks > 0 {
		fmt.Fprintf(cmd.w, "%d check(s) failed, but none are fatal.\n", failedChecks)
	} else {
		fmt.Fprintf(cmd.w, "All checks passed.\n")
	}

	return nil
}

func (cmd *checkSubcommand) printCheckDetails(check *service.Check) {
	if cmd.quiet {
		fmt.Fprintf(cmd.w, "Checking %s...", check.Name)
		return
	}

	fmt.Fprintf(cmd.w, "Checking %s - %s [%s]\n", check.Name, check.Description, check.Severity)
}
