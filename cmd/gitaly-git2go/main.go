//go:build static && system_libgit2

package main

import (
	"context"
	"encoding/gob"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	git "github.com/libgit2/git2go/v34"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git2go"
	glog "gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/labkit/correlation"
)

type subcmd interface {
	Flags() *flag.FlagSet
	Run(ctx context.Context, decoder *gob.Decoder, encoder *gob.Encoder) error
}

var subcommands = map[string]subcmd{
	"cherry-pick": &cherryPickSubcommand{},
	"rebase":      &rebaseSubcommand{},
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

func main() {
	decoder := gob.NewDecoder(os.Stdin)
	encoder := gob.NewEncoder(os.Stdout)

	var logFormat, logLevel, correlationID string
	var enabledFeatureFlags, disabledFeatureFlags featureFlagArg

	flags := flag.NewFlagSet(git2go.BinaryName, flag.PanicOnError)
	flags.StringVar(&logFormat, "log-format", "", "logging format")
	flags.StringVar(&logLevel, "log-level", "", "logging level")
	flags.StringVar(&correlationID, "correlation-id", "", "correlation ID used for request tracing")
	flags.Var(
		&enabledFeatureFlags,
		"enabled-feature-flags",
		"comma separated list of explicitly enabled feature flags",
	)
	flags.Var(
		&disabledFeatureFlags,
		"disabled-feature-flags",
		"comma separated list of explicitly disabled feature flags",
	)
	_ = flags.Parse(os.Args[1:])

	if correlationID == "" {
		correlationID = correlation.SafeRandomID()
	}

	glog.Configure(os.Stderr, logFormat, logLevel)

	ctx := correlation.ContextWithCorrelation(context.Background(), correlationID)
	logger := glog.Default().WithFields(logrus.Fields{
		"command.name":           git2go.BinaryName,
		"correlation_id":         correlationID,
		"enabled_feature_flags":  enabledFeatureFlags,
		"disabled_feature_flags": disabledFeatureFlags,
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

	for _, configLevel := range []git.ConfigLevel{
		git.ConfigLevelSystem,
		git.ConfigLevelXDG,
		git.ConfigLevelGlobal,
	} {
		if err := git.SetSearchPath(configLevel, "/dev/null"); err != nil {
			fatalf(logger, encoder, "setting search path: %s", err)
		}
	}

	subcmdLogger := logger.WithField("command.subcommand", subcmdFlags.Name())
	subcmdLogger.Infof("starting %s command", subcmdFlags.Name())

	ctx = ctxlogrus.ToContext(ctx, subcmdLogger)
	ctx = enabledFeatureFlags.ToContext(ctx, true)
	ctx = disabledFeatureFlags.ToContext(ctx, false)

	if err := subcmd.Run(ctx, decoder, encoder); err != nil {
		subcmdLogger.WithError(err).Errorf("%s command failed", subcmdFlags.Name())
		fatalf(logger, encoder, "%s: %s", subcmdFlags.Name(), err)
	}

	subcmdLogger.Infof("%s command finished", subcmdFlags.Name())
}

type featureFlagArg []featureflag.FeatureFlag

func (v *featureFlagArg) String() string {
	metadataKeys := make([]string, 0, len(*v))
	for _, flag := range *v {
		metadataKeys = append(metadataKeys, flag.MetadataKey())
	}
	return strings.Join(metadataKeys, ",")
}

func (v *featureFlagArg) Set(s string) error {
	if s == "" {
		return nil
	}

	for _, metadataKey := range strings.Split(s, ",") {
		flag, err := featureflag.FromMetadataKey(metadataKey)
		if err != nil {
			return err
		}

		*v = append(*v, flag)
	}

	return nil
}

func (v featureFlagArg) ToContext(ctx context.Context, enabled bool) context.Context {
	for _, flag := range v {
		ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, flag, enabled)
	}

	return ctx
}
