package praefect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"time"

	"github.com/urfave/cli/v2"
	glcli "gitlab.com/gitlab-org/gitaly/v16/internal/cli"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/correlation"
	"google.golang.org/grpc/metadata"
)

const (
	trackRepositoryCmdName = "track-repository"
)

func newTrackRepositoryCommand() *cli.Command {
	return &cli.Command{
		Name:  trackRepositoryCmdName,
		Usage: "start tracking a repository",
		Description: "This command adds a given repository to be tracked by Praefect.\n" +
			"It checks if the repository exists on disk on the authoritative storage,\n" +
			"and whether database records are absent from tracking the repository.\n" +
			"If the 'replicate-immediately' flag is used, the command will attempt to replicate\n" +
			"the repository to the secondaries. The command is blocked until the\n" +
			"replication finishes. Otherwise, replication jobs will be created and will " +
			"be executed eventually by Praefect in the background.\n",
		HideHelpCommand: true,
		Action:          trackRepositoryAction,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     paramVirtualStorage,
				Usage:    "name of the repository's virtual storage",
				Required: true,
			},
			&cli.StringFlag{
				Name:     paramRelativePath,
				Usage:    "relative path of the repository on the virtual storage. Usually starts with '@hashed'",
				Required: true,
			},
			&cli.StringFlag{
				Name:     paramReplicaPath,
				Usage:    "path of the repository on the physical storage. Can start with '@cluster' or match the relative path",
				Required: true,
			},
			&cli.StringFlag{
				Name:  paramAuthoritativeStorage,
				Usage: "physical storage to consider as authoritative for the repository",
			},
			&cli.BoolFlag{
				Name:  "replicate-immediately",
				Usage: "kick off a replication immediately",
			},
		},
		Before: func(ctx *cli.Context) error {
			if ctx.Args().Present() {
				_ = cli.ShowSubcommandHelp(ctx)
				return cli.Exit(unexpectedPositionalArgsError{Command: ctx.Command.Name}, 1)
			}
			return nil
		},
	}
}

type trackRepositoryRequest struct {
	RelativePath         string `json:"relative_path"`
	ReplicaPath          string `json:"replica_path"`
	VirtualStorage       string `json:"virtual_storage"`
	AuthoritativeStorage string `json:"authoritative_storage"`
}

var errAuthoritativeRepositoryNotExist = errors.New("authoritative repository does not exist")

func trackRepositoryAction(appCtx *cli.Context) error {
	logger := log.ConfigureCommand()

	conf, err := readConfig(appCtx.String(configFlagName))
	if err != nil {
		return err
	}

	virtualStorage := appCtx.String(paramVirtualStorage)
	relativePath := appCtx.String(paramRelativePath)
	replicaPath := appCtx.String(paramReplicaPath)
	authoritativeStorage := appCtx.String(paramAuthoritativeStorage)
	replicateImmediately := appCtx.Bool("replicate-immediately")

	if authoritativeStorage == "" {
		if conf.Failover.ElectionStrategy == config.ElectionStrategyPerRepository {
			return glcli.RequiredFlagError(paramAuthoritativeStorage)
		}
	}

	ctx := correlation.ContextWithCorrelation(context.Background(), correlation.SafeRandomID())

	openDBCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := glsql.OpenDB(openDBCtx, conf.DB)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer func() { _ = db.Close() }()

	req := trackRepositoryRequest{
		RelativePath:         relativePath,
		ReplicaPath:          replicaPath,
		AuthoritativeStorage: authoritativeStorage,
		VirtualStorage:       virtualStorage,
	}

	logger = logger.WithField("correlation_id", correlation.ExtractFromContext(ctx))
	return req.execRequest(ctx, db, conf, appCtx.App.Writer, logger, replicateImmediately)
}

const trackRepoErrorPrefix = "attempting to track repository in praefect database"

func (req *trackRepositoryRequest) execRequest(ctx context.Context,
	db *sql.DB,
	cfg config.Config,
	w io.Writer,
	logger log.Logger,
	replicateImmediately bool,
) error {
	logger.WithFields(log.Fields{
		"virtual_storage":       req.VirtualStorage,
		"relative_path":         req.RelativePath,
		"replica_path":          req.ReplicaPath,
		"authoritative_storage": req.AuthoritativeStorage,
	}).Debug("track repository")

	var primary string
	var secondaries []string
	var variableReplicationFactorEnabled, savePrimary bool
	if cfg.Failover.ElectionStrategy == config.ElectionStrategyPerRepository {
		savePrimary = true
		primary = req.AuthoritativeStorage

		for _, vs := range cfg.VirtualStorages {
			if vs.Name == req.VirtualStorage {
				for _, node := range vs.Nodes {
					if node.Storage == req.AuthoritativeStorage {
						continue
					}
					secondaries = append(secondaries, node.Storage)
				}
			}
		}

		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		replicationFactor := cfg.DefaultReplicationFactors()[req.VirtualStorage]

		if replicationFactor > 0 {
			variableReplicationFactorEnabled = true
			// Select random secondaries according to the default replication factor.
			r.Shuffle(len(secondaries), func(i, j int) {
				secondaries[i], secondaries[j] = secondaries[j], secondaries[i]
			})

			secondaries = secondaries[:replicationFactor-1]
		}
	} else {
		savePrimary = false
		if err := db.QueryRowContext(ctx, `SELECT node_name FROM shard_primaries WHERE shard_name = $1 AND demoted = 'false'`, req.VirtualStorage).Scan(&primary); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%s: no primaries found", trackRepoErrorPrefix)
			}
			return fmt.Errorf("%s: %w", trackRepoErrorPrefix, err)
		}
	}

	authoritativeRepoExists, err := req.authoritativeRepositoryExists(ctx, cfg, logger, w, primary)
	if err != nil {
		return fmt.Errorf("%s: %w", trackRepoErrorPrefix, err)
	}

	if !authoritativeRepoExists {
		return fmt.Errorf("%s: %w", trackRepoErrorPrefix, errAuthoritativeRepositoryNotExist)
	}

	nodeSet, err := praefect.DialNodes(
		ctx,
		cfg.VirtualStorages,
		protoregistry.GitalyProtoPreregistered,
		nil,
		nil,
		nil,
		logger,
	)
	if err != nil {
		return fmt.Errorf("%s: %w", trackRepoErrorPrefix, err)
	}
	defer nodeSet.Close()

	store := datastore.NewPostgresRepositoryStore(db, cfg.StorageNames())
	queue := datastore.NewPostgresReplicationEventQueue(db)
	replMgr := praefect.NewReplMgr(
		logger,
		cfg.StorageNames(),
		queue,
		store,
		praefect.StaticHealthChecker(cfg.StorageNames()),
		nodeSet,
	)

	repositoryID, err := req.trackRepository(
		ctx,
		store,
		w,
		primary,
		secondaries,
		savePrimary,
		variableReplicationFactorEnabled,
	)
	if err != nil {
		return fmt.Errorf("%s: %w", trackRepoErrorPrefix, err)
	}

	fmt.Fprintln(w, "Finished adding new repository to be tracked in praefect database.")

	correlationID := correlation.SafeRandomID()
	connections := nodeSet.Connections()[req.VirtualStorage]

	for _, secondary := range secondaries {
		event := datastore.ReplicationEvent{
			Job: datastore.ReplicationJob{
				RepositoryID:      repositoryID,
				Change:            datastore.UpdateRepo,
				RelativePath:      req.RelativePath,
				ReplicaPath:       req.ReplicaPath,
				VirtualStorage:    req.VirtualStorage,
				SourceNodeStorage: primary,
				TargetNodeStorage: secondary,
			},
			Meta: datastore.Params{datastore.CorrelationIDKey: correlationID},
		}
		if replicateImmediately {
			conn, ok := connections[secondary]
			if !ok {
				return fmt.Errorf("%s: connection for %q not found", trackRepoErrorPrefix, secondary)
			}

			if err := replMgr.ProcessReplicationEvent(ctx, event, conn); err != nil {
				return fmt.Errorf("%s: processing replication event %w", trackRepoErrorPrefix, err)
			}

			fmt.Fprintf(w, "Finished replicating repository to %q.\n", secondary)
			continue
		}

		if _, err := queue.Enqueue(ctx, event); err != nil {
			if errors.As(err, &datastore.ReplicationEventExistsError{}) {
				fmt.Fprintf(w, "replication event queue already has similar entry: %s.\n", err)
				return nil
			}

			return fmt.Errorf("%s: %w", trackRepoErrorPrefix, err)
		}
		fmt.Fprintf(w, "Added replication job to replicate repository to %q.\n", secondary)
	}

	return nil
}

func (req *trackRepositoryRequest) trackRepository(
	ctx context.Context,
	ds *datastore.PostgresRepositoryStore,
	w io.Writer,
	primary string,
	secondaries []string,
	savePrimary bool,
	variableReplicationFactorEnabled bool,
) (int64, error) {
	repositoryID, err := ds.ReserveRepositoryID(ctx, req.VirtualStorage, req.RelativePath)
	if err != nil {
		if errors.Is(err, datastore.ErrRepositoryAlreadyExists) {
			fmt.Fprintf(w, "repository is already tracked in praefect database")
			existingID, err := ds.GetRepositoryID(ctx, req.VirtualStorage, req.RelativePath)
			if err != nil {
				return 0, fmt.Errorf("GetRepositoryID: %w", err)
			}
			return existingID, nil
		}

		return 0, fmt.Errorf("ReserveRepositoryID: %w", err)
	}

	if err := ds.CreateRepository(
		ctx,
		repositoryID,
		req.VirtualStorage,
		req.RelativePath,
		req.ReplicaPath,
		primary,
		nil,
		secondaries,
		savePrimary,
		variableReplicationFactorEnabled,
	); err != nil {
		return 0, fmt.Errorf("CreateRepository: %w", err)
	}

	return repositoryID, nil
}

func repositoryExists(ctx context.Context, repo *gitalypb.Repository, addr, token string) (bool, error) {
	conn, err := subCmdDial(ctx, addr, token, defaultDialTimeout)
	if err != nil {
		return false, fmt.Errorf("error dialing: %w", err)
	}
	defer func() { _ = conn.Close() }()

	ctx = metadata.AppendToOutgoingContext(ctx, "client_name", trackRepositoryCmdName)
	repositoryClient := gitalypb.NewRepositoryServiceClient(conn)
	res, err := repositoryClient.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{Repository: repo})
	if err != nil {
		return false, err
	}

	return res.GetExists(), nil
}

func (req *trackRepositoryRequest) authoritativeRepositoryExists(ctx context.Context, cfg config.Config, logger log.Logger, w io.Writer, nodeName string) (bool, error) {
	for _, vs := range cfg.VirtualStorages {
		if vs.Name != req.VirtualStorage {
			continue
		}

		for _, node := range vs.Nodes {
			if node.Storage == nodeName {
				logger.WithFields(log.Fields{
					"replica_path": req.ReplicaPath,
					"storage_node": node.Storage,
					"node_address": node.Address,
				}).Debug("check if repository exists on Gitaly node")

				repo := &gitalypb.Repository{
					StorageName:  node.Storage,
					RelativePath: req.ReplicaPath,
				}
				exists, err := repositoryExists(ctx, repo, node.Address, node.Token)
				if err != nil {
					fmt.Fprintf(w, "checking if repository exists %q, %q\n", node.Storage, req.ReplicaPath)
					return false, nil
				}
				return exists, nil
			}
		}
		return false, fmt.Errorf("node %q not found", req.AuthoritativeStorage)
	}
	return false, fmt.Errorf("virtual storage %q not found", req.VirtualStorage)
}
