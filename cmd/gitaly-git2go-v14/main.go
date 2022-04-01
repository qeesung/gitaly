//go:build static && system_libgit2
// +build static,system_libgit2

package main

import (
	"context"
	"encoding/gob"
	"flag"
	"fmt"
	"os"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	git "github.com/libgit2/git2go/v33"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git2go"
	glog "gitlab.com/gitlab-org/gitaly/v14/internal/log"
	"gitlab.com/gitlab-org/labkit/correlation"
)

type subcmd interface {
	Flags() *flag.FlagSet
	Run(ctx context.Context, decoder *gob.Decoder, encoder *gob.Encoder) error
}

var subcommands = map[string]subcmd{
	"apply":       &applySubcommand{},
	"cherry-pick": &cherryPickSubcommand{},
	"commit":      commitSubcommand{},
	"conflicts":   &conflictsSubcommand{},
	"merge":       &mergeSubcommand{},
	"rebase":      &rebaseSubcommand{},
	"revert":      &revertSubcommand{},
	"resolve":     &resolveSubcommand{},
	"submodule":   &submoduleSubcommand{},
}

func fatalf(logger logrus.FieldLogger, encoder *gob.Encoder, format string, args ...interface{}) {
	err := encoder.Encode(git2go.Result{
		Err: git2go.SerializableError(fmt.Errorf(format, args...)),
	})
	if err != nil {
		logger.WithError(err).Error("encode to gob failed")
	}
	// An exit code of 1 would indicate an error over stderr. Since our errors
	// are encoded over gob, we need to exit cleanly
	os.Exit(0)
}

func configureLogging(format, level string) {
	// Gitaly logging by default goes to stdout, which would interfere with gob
	// encoding.
	for _, l := range glog.Loggers {
		l.Out = os.Stderr
	}
	glog.Configure(glog.Loggers, format, level)
}

func main() {
	decoder := gob.NewDecoder(os.Stdin)
	encoder := gob.NewEncoder(os.Stdout)

	var logFormat, logLevel, correlationID string

	flags := flag.NewFlagSet(git2go.BinaryName, flag.PanicOnError)
	flags.StringVar(&logFormat, "log-format", "", "logging format")
	flags.StringVar(&logLevel, "log-level", "", "logging level")
	flags.StringVar(&correlationID, "correlation-id", "", "correlation ID used for request tracing")
	_ = flags.Parse(os.Args[1:])

	if correlationID == "" {
		correlationID = correlation.SafeRandomID()
	}

	configureLogging(logFormat, logLevel)

	ctx := correlation.ContextWithCorrelation(context.Background(), correlationID)
	logger := glog.Default().WithFields(logrus.Fields{
		"command.name":   git2go.BinaryName,
		"correlation_id": correlationID,
	})

	if flags.NArg() < 1 {
		fatalf(logger, encoder, "missing subcommand")
	}

	subcmd, ok := subcommands[flags.Arg(0)]
	if !ok {
		fatalf(logger, encoder, "unknown subcommand: %q", flags.Arg(0))
	}

	subcmdFlags := subcmd.Flags()
	if err := subcmdFlags.Parse(flags.Args()[1:]); err != nil {
		fatalf(logger, encoder, "parsing flags of %q: %s", subcmdFlags.Name(), err)
	}

	if subcmdFlags.NArg() != 0 {
		fatalf(logger, encoder, "%s: trailing arguments", subcmdFlags.Name())
	}

	if err := git.EnableFsyncGitDir(true); err != nil {
		fatalf(logger, encoder, "enable fsync: %s", err)
	}

	subcmdLogger := logger.WithField("command.subcommand", subcmdFlags.Name())
	subcmdLogger.Infof("starting %s command", subcmdFlags.Name())

	ctx = ctxlogrus.ToContext(ctx, subcmdLogger)
	if err := subcmd.Run(ctx, decoder, encoder); err != nil {
		subcmdLogger.WithError(err).Errorf("%s command failed", subcmdFlags.Name())
		fatalf(logger, encoder, "%s: %s", subcmdFlags.Name(), err)
	}

	subcmdLogger.Infof("%s command finished", subcmdFlags.Name())
}
