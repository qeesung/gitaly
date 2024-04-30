package gitaly

import (
	"fmt"
	"time"

	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func newBundleURICommand() *cli.Command {
	return &cli.Command{
		Name:  "bundle-uri",
		Usage: "Generate bundle URI bundle",
		UsageText: `gitaly bundle-uri --storage=<storage-name> --repository=<relative-path> --config=<gitaly_config_file>

Example: gitaly bundle-uri --storage=default --repository=ab/cd/ef012345678901234567890 --config=config.toml`,
		Description: "Generate a bundle for bundle-URI for the given repository.",
		Action:      bundleURIAction,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  flagStorage,
				Usage: "storage containing the repository",
			},
			&cli.StringFlag{
				Name:     flagRepository,
				Usage:    "repository to generate bundle-URI for",
				Required: true,
			},
			&cli.StringFlag{
				Name:     flagConfig,
				Usage:    "path to Gitaly configuration",
				Aliases:  []string{"c"},
				Required: true,
			},
		},
	}
}

func bundleURIAction(ctx *cli.Context) error {
	log.ConfigureCommand()

	cfg, err := loadConfig(ctx.String(flagConfig))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

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
		return fmt.Errorf("connect to Gitaly: %w", err)
	}
	defer conn.Close()

	req := gitalypb.GenerateBundleURIRequest{
		Repository: &gitalypb.Repository{
			StorageName:  storage,
			RelativePath: ctx.String(flagRepository),
		},
	}

	repoClient := gitalypb.NewRepositoryServiceClient(conn)
	_, err = repoClient.GenerateBundleURI(ctx.Context, &req)

	return err
}
