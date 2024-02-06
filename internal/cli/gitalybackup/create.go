package gitalybackup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"time"

	cli "github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type serverRepository struct {
	storage.ServerInfo
	StorageName   string `json:"storage_name"`
	RelativePath  string `json:"relative_path"`
	GlProjectPath string `json:"gl_project_path"`
}

type createSubcommand struct {
	backupPath      string
	parallel        int
	parallelStorage int
	layout          string
	incremental     bool
	backupID        string
	serverSide      bool
}

func (cmd *createSubcommand) flags(ctx *cli.Context) {
	cmd.backupPath = ctx.String("path")
	cmd.parallel = ctx.Int("parallel")
	cmd.parallelStorage = ctx.Int("parallel-storage")
	cmd.layout = ctx.String("layout")
	cmd.incremental = ctx.Bool("incremental")
	cmd.backupID = ctx.String("id")
	cmd.serverSide = ctx.Bool("server-side")
}

func createFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  "path",
			Usage: "repository backup path",
		},
		&cli.IntFlag{
			Name:  "parallel",
			Usage: "maximum number of parallel backups",
			Value: runtime.NumCPU(),
		},
		&cli.IntFlag{
			Name:  "parallel-storage",
			Usage: "maximum number of parallel backups per storage. Note: actual parallelism when combined with `-parallel` depends on the order the repositories are received.",
			Value: 2,
		},
		&cli.StringFlag{
			Name:  "layout",
			Usage: "how backup files are located. Either pointer or legacy.",
			Value: "pointer",
		},
		&cli.BoolFlag{
			Name:  "incremental",
			Usage: "creates an incremental backup if possible.",
			Value: false,
		},
		&cli.StringFlag{
			Name:  "id",
			Usage: "the backup ID used when creating a full backup.",
			Value: time.Now().UTC().Format("20060102150405"),
		},
		&cli.BoolFlag{
			Name:  "server-side",
			Usage: "use server-side backups. Note: The feature is not ready for production use.",
			Value: false,
		},
	}
}

func newCreateCommand() *cli.Command {
	return &cli.Command{
		Name:   "create",
		Usage:  "Create backup file",
		Action: createAction,
		Flags:  createFlags(),
	}
}

func createAction(cctx *cli.Context) error {
	logger, err := log.Configure(cctx.App.Writer, "json", "")
	if err != nil {
		fmt.Printf("configuring logger failed: %v", err)
		return err
	}

	ctx, err := storage.InjectGitalyServersEnv(context.Background())
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	subcmd := createSubcommand{}

	subcmd.flags(cctx)

	if err := subcmd.run(ctx, logger, cctx.App.Reader); err != nil {
		logger.Error(err.Error())
		return err
	}
	return nil
}

func (cmd *createSubcommand) run(ctx context.Context, logger log.Logger, stdin io.Reader) error {
	pool := client.NewPool(client.WithDialOptions(client.UnaryInterceptor(), client.StreamInterceptor()))
	defer func() {
		_ = pool.Close()
	}()

	var manager backup.Strategy
	if cmd.serverSide {
		if cmd.backupPath != "" {
			return fmt.Errorf("create: path cannot be used with server-side backups")
		}

		manager = backup.NewServerSideAdapter(pool)
	} else {
		sink, err := backup.ResolveSink(ctx, cmd.backupPath)
		if err != nil {
			return fmt.Errorf("create: resolve sink: %w", err)
		}

		locator, err := backup.ResolveLocator(cmd.layout, sink)
		if err != nil {
			return fmt.Errorf("create: resolve locator: %w", err)
		}

		manager = backup.NewManager(sink, logger, locator, pool)
	}

	var opts []backup.PipelineOption
	if cmd.parallel > 0 || cmd.parallelStorage > 0 {
		opts = append(opts, backup.WithConcurrency(cmd.parallel, cmd.parallelStorage))
	}
	pipeline, err := backup.NewPipeline(logger, opts...)
	if err != nil {
		return fmt.Errorf("create pipeline: %w", err)
	}

	decoder := json.NewDecoder(stdin)
	for {
		var sr serverRepository
		if err := decoder.Decode(&sr); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("create: %w", err)
		}
		repo := gitalypb.Repository{
			StorageName:   sr.StorageName,
			RelativePath:  sr.RelativePath,
			GlProjectPath: sr.GlProjectPath,
		}
		pipeline.Handle(ctx, backup.NewCreateCommand(manager, backup.CreateRequest{
			Server:           sr.ServerInfo,
			Repository:       &repo,
			VanityRepository: &repo,
			Incremental:      cmd.incremental,
			BackupID:         cmd.backupID,
		}))
	}

	if _, err := pipeline.Done(); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	return nil
}
