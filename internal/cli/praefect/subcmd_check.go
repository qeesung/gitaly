package praefect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/service"
)

func newCheckCommand(checkFuncs []service.CheckFunc) *cli.Command {
	return &cli.Command{
		Name:  "check",
		Usage: "run startup checks",
		Description: `Run Praefect startup checks.

Example: praefect --config praefect.config.toml check`,
		HideHelpCommand: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "q",
				Usage: "do not print verbose output for each check",
			},
		},
		Action: checkAction(checkFuncs),
		Before: func(ctx *cli.Context) error {
			if ctx.Args().Present() {
				_ = cli.ShowSubcommandHelp(ctx)
				return cli.Exit(unexpectedPositionalArgsError{Command: ctx.Command.Name}, 1)
			}
			return nil
		},
	}
}

var errFatalChecksFailed = errors.New("checks failed")

func checkAction(checkFuncs []service.CheckFunc) func(ctx *cli.Context) error {
	return func(ctx *cli.Context) error {
		logger := log.Default()
		conf, err := getConfig(logger, ctx.String(configFlagName))
		if err != nil {
			return err
		}

		quiet := ctx.Bool("q")
		var allChecks []*service.Check
		for _, checkFunc := range checkFuncs {
			allChecks = append(allChecks, checkFunc(conf, ctx.App.Writer, quiet))
		}

		passed := true
		var failedChecks int
		for _, check := range allChecks {
			func() {
				timeCtx, cancel := context.WithTimeout(ctx.Context, 5*time.Second)
				defer cancel()

				printCheckDetails(quiet, ctx.App.Writer, check)

				if err := check.Run(timeCtx); err != nil {
					failedChecks++
					if check.Severity == service.Fatal {
						passed = false
					}
					fmt.Fprintf(ctx.App.Writer, "Failed (%s) error: %s\n", check.Severity, err.Error())
				} else {
					fmt.Fprintf(ctx.App.Writer, "Passed\n")
				}
			}()
		}

		fmt.Fprintf(ctx.App.Writer, "\n")

		if !passed {
			fmt.Fprintf(ctx.App.Writer, "%d check(s) failed, at least one was fatal.\n", failedChecks)
			return errFatalChecksFailed
		}

		if failedChecks > 0 {
			fmt.Fprintf(ctx.App.Writer, "%d check(s) failed, but none are fatal.\n", failedChecks)
		} else {
			fmt.Fprintf(ctx.App.Writer, "All checks passed.\n")
		}

		return nil
	}
}

func printCheckDetails(quiet bool, w io.Writer, check *service.Check) {
	if quiet {
		fmt.Fprintf(w, "Checking %s...", check.Name)
		return
	}

	fmt.Fprintf(w, "Checking %s - %s [%s]\n", check.Name, check.Description, check.Severity)
}
