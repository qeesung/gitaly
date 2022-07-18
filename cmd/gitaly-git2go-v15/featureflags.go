//go:build static && system_libgit2 && gitaly_test

package main

import (
	"context"
	"encoding/gob"
	"flag"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
)

// This subcommand is only called in tests, so we don't want to register it like
// the other subcommands but instead will do it in an init block. The gitaly_test build
// flag will guarantee that this is not built and registered in the
// gitaly-git2go binary
func init() {
	subcommands["feature-flags"] = &featureFlagsSubcommand{}
}

type featureFlagsSubcommand struct{}

func (featureFlagsSubcommand) Flags() *flag.FlagSet {
	return flag.NewFlagSet("feature-flags", flag.ExitOnError)
}

func (featureFlagsSubcommand) Run(ctx context.Context, decoder *gob.Decoder, encoder *gob.Encoder) error {
	var flags []git2go.FeatureFlag
	for flag, value := range featureflag.FromContext(ctx) {
		flags = append(flags, git2go.FeatureFlag{
			Name:        flag.Name,
			MetadataKey: flag.MetadataKey(),
			Value:       value,
		})
	}

	return encoder.Encode(git2go.FeatureFlags{
		Flags: flags,
	})
}
