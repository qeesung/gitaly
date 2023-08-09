package gitaly

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/urfave/cli/v2"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v16/auth"
	"gitlab.com/gitlab-org/gitaly/v16/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	internalclient "gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	gitalylog "gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc"
)

const (
	flagStorage    = "storage"
	flagRepository = "repository"
	flagConfig     = "config"
)

func newHooksCommand() *cli.Command {
	return &cli.Command{
		Name:  "hooks",
		Usage: "manage Git hooks",
		Description: `Manage hooks for a Git repository.

Provides the following subcommand:

- set`,
		HideHelpCommand: true,
		Subcommands: []*cli.Command{
			{
				Name:  "set",
				Usage: "set custom hooks for a Git repository",
				Description: `Reads a tarball containing custom Git hooks from stdin and writes the hooks to the specified repository.

Example: gitaly hooks set --storage default --repository @hashed/<path_to_git_repository>/repository.git --config gitaly.config.toml < hooks_tarball.tar

To remove custom Git hooks for a specified repository, run the set subcommand with an empty tarball file.`,
				Action: setHooksAction,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  flagStorage,
						Usage: "storage containing the repository",
					},
					&cli.StringFlag{
						Name:     flagRepository,
						Usage:    "repository to set hooks for",
						Required: true,
					},
					&cli.StringFlag{
						Name:     flagConfig,
						Usage:    "path to Gitaly configuration",
						Aliases:  []string{"c"},
						Required: true,
					},
				},
			},
		},
	}
}

func setHooksAction(ctx *cli.Context) error {
	cfg, err := loadConfig(ctx.String(flagConfig))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	gitalylog.Configure(os.Stdout, cfg.Logging.Format, cfg.Logging.Level)

	storage := ctx.String(flagStorage)
	if storage == "" {
		if len(cfg.Storages) != 1 {
			return fmt.Errorf("multiple storages configured: use --storage to target storage explicitly")
		}

		storage = cfg.Storages[0].Name
	}

	address, err := getAddressWithScheme(cfg)
	if err != nil {
		return fmt.Errorf("get Gitaly address: %w", err)
	}

	conn, err := dial(ctx.Context, address, cfg.Auth.Token, 10*time.Second)
	if err != nil {
		return fmt.Errorf("create connection: %w", err)
	}
	defer conn.Close()

	if err := setRepoHooks(ctx.Context, conn,
		ctx.App.Reader,
		storage,
		ctx.String(flagRepository),
	); err != nil {
		return err
	}

	return nil
}

// setRepoHooks sets custom hooks for the specified repository. The specified reader is expected to
// provide a tarball containing custom git hooks within a `custom_hooks` directory.
func setRepoHooks(ctx context.Context, conn *grpc.ClientConn, reader io.Reader, storage, relativePath string) error {
	repoClient := gitalypb.NewRepositoryServiceClient(conn)
	stream, err := repoClient.SetCustomHooks(ctx)
	if err != nil {
		return fmt.Errorf("create repository client: %w", err)
	}

	// Send first request containing only repository information.
	if err := stream.Send(&gitalypb.SetCustomHooksRequest{
		Repository: &gitalypb.Repository{
			StorageName:  storage,
			RelativePath: relativePath,
		},
	}); err != nil {
		return err
	}

	// Configure streamWriter to transmit tarball data to stream.
	streamWriter := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.SetCustomHooksRequest{Data: p})
	})

	if _, err := io.Copy(streamWriter, reader); err != nil {
		// Ignore EOF errors to avoid race caused by server closing stream
		// prematurely. This allows us to get an accurate error message as to
		// why the stream was closed.
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("copying hooks archive: %w", err)
		}
	}

	if _, err := stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("closing hooks archive stream: %w", err)
	}

	return nil
}

func dial(ctx context.Context, addr, token string, timeout time.Duration, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
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

func getAddressWithScheme(cfg config.Cfg) (string, error) {
	switch {
	case cfg.SocketPath != "":
		return "unix:" + cfg.SocketPath, nil
	case cfg.ListenAddr != "":
		return "tcp://" + cfg.ListenAddr, nil
	case cfg.TLSListenAddr != "":
		return "tls://" + cfg.TLSListenAddr, nil
	default:
		return "", errors.New("no address configured")
	}
}
