package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/signature"
)

func gpgApp() *cli.App {
	return &cli.App{
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "status-fd"},
			&cli.BoolFlag{Name: "verify"},
			&cli.BoolFlag{Name: "bsau"},
		},
		Action: func(cCtx *cli.Context) error {
			// Git passes the --status-fd=2 flag into the gpg call.
			if cCtx.Int("status-fd") != 2 {
				return errors.New("expected --status-fd=2")
			}

			signingKeys, err := signature.ParseSigningKeys(cCtx.Args().First())
			if err != nil {
				return fmt.Errorf("reading signed key file %s : %w", cCtx.Args().First(), err)
			}

			contents, err := io.ReadAll(cCtx.App.Reader)
			if err != nil {
				return fmt.Errorf("reading contents from stdin: %w", err)
			}

			// Signing a commit can be nondeterministic if not done at the same second
			// We use `GIT_AUTHOR_DATE` to ensure that the signature is deterministic
			date := time.Now()
			authorDate, exists := os.LookupEnv("GIT_AUTHOR_DATE")
			if exists {
				date, err = time.ParseInLocation(git.Rfc2822DateFormat, authorDate, time.FixedZone("", 0))
				if err != nil {
					return fmt.Errorf("error parsing GIT_AUTHOR_DATE: %w", err)
				}
			}

			sig, err := signingKeys.CreateSignature(contents, date)
			if err != nil {
				return fmt.Errorf("creating signature: %w", err)
			}

			// Git looks for this output string as part of GPG output.
			if _, err := cCtx.App.ErrWriter.Write([]byte("[GNUPG:] SIG_CREATED ")); err != nil {
				return fmt.Errorf("printing to stdout: %w", err)
			}

			if _, err := cCtx.App.Writer.Write(sig); err != nil {
				return fmt.Errorf("printing to stdout: %w", err)
			}

			if _, err := cCtx.App.Writer.Write([]byte("\n")); err != nil {
				return fmt.Errorf("printing to stdout: %w", err)
			}

			return nil
		},
	}
}

// This binary is used to support signing through providing the path to the
// actual signing key. Git itself does not support this since it simply calls
// out to gpg(1), which only supports fetching keys from the gpg database.
// This binary is used as a stopgap measure since we can set Git's gpg.program
// config to point to this binary, which interprets the key_id passed in as the
// path to the signing key.
// In the future, we will modify Git so that commit-tree can take in a raw
// commit message that we can add a signature to, at which point we can sign
// commits manually and get rid of this binary.
func main() {
	if err := gpgApp().Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
