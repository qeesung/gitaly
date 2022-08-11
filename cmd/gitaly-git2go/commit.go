//go:build static && system_libgit2

package main

import (
	"context"
	"encoding/gob"
	"flag"

	"gitlab.com/gitlab-org/gitaly/v15/cmd/gitaly-git2go/commit"
	"gitlab.com/gitlab-org/gitaly/v15/cmd/gitaly-git2go/git2goutil"
)

type commitSubcommand struct {
	signingKeyPath string
}

func (cmd *commitSubcommand) Flags() *flag.FlagSet {
	fs := flag.NewFlagSet("commit", flag.ExitOnError)
	fs.StringVar(&cmd.signingKeyPath, "signing-key", "", "Path to the OpenPGP signing key.")
	return fs
}

func (cmd *commitSubcommand) Run(ctx context.Context, decoder *gob.Decoder, encoder *gob.Encoder) error {
	ctx = context.WithValue(ctx, git2goutil.SigningKeyPathKey{}, cmd.signingKeyPath)
	return commit.Run(ctx, decoder, encoder)
}
