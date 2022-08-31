package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"time"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/metadatahandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/commonerr"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/correlation"
	"google.golang.org/grpc/metadata"
)

const (
	trackRepositoryCmdName = "track-repository"
)

type trackRepository struct {
	w                    io.Writer
	logger               logrus.FieldLogger
	virtualStorage       string
	relativePath         string
	authoritativeStorage string
	replicateImmediately bool
}

type trackRepositoryRequest struct {
	RelativePath         string `json:"relative_path"`
	VirtualStorage       string `json:"virtual_storage"`
	AuthoritativeStorage string `json:"authoritative_storage"`
}

var errAuthoritativeRepositoryNotExist = errors.New("authoritative repository does not exist")

func newTrackRepository(logger logrus.FieldLogger, w io.Writer) *trackRepository {
	return &trackRepository{w: w, logger: logger}
}

func (cmd *trackRepository) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(trackRepositoryCmdName, flag.ExitOnError)
	fs.StringVar(&cmd.virtualStorage, paramVirtualStorage, "", "name of the repository's virtual storage")
	fs.StringVar(&cmd.relativePath, paramRelativePath, "", "relative path to the repository")
	fs.StringVar(&cmd.authoritativeStorage, paramAuthoritativeStorage, "", "storage with the repository to consider as authoritative")
	fs.BoolVar(&cmd.replicateImmediately, "replicate-immediately", false, "kick off a replication immediately")
	fs.Usage = func() {
		printfErr("Description:\n" +
			"	This command adds a given repository to be tracked by Praefect.\n" +
			"	It checks if the repository exists on disk on the authoritative storage,\n" +
			"	and whether database records are absent from tracking the repository.\n" +
			"	If -replicate-immediately is used, the command will attempt to replicate the repository to the secondaries.\n" +
			"	Otherwise, replication jobs will be created and will be executed eventually by Praefect itself.\n")
		fs.PrintDefaults()
	}
	return fs
}

func (cmd trackRepository) Exec(flags *flag.FlagSet, cfg config.Config) error {
	switch {
	case flags.NArg() > 0:
		return unexpectedPositionalArgsError{Command: flags.Name()}
	case cmd.virtualStorage == "":
		return requiredParameterError(paramVirtualStorage)
	case cmd.relativePath == "":
		return requiredParameterError(paramRelativePath)
	case cmd.authoritativeStorage == "":
		if cfg.Failover.ElectionStrategy == config.ElectionStrategyPerRepository {
			return requiredParameterError(paramAuthoritativeStorage)
		}
	}

	ctx := correlation.ContextWithCorrelation(context.Background(), correlation.SafeRandomID())

	openDBCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := glsql.OpenDB(openDBCtx, cfg.DB)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer func() { _ = db.Close() }()

	return cmd.exec(ctx, db, cfg)
}

const trackRepoErrorPrefix = "attempting to track repository in praefect database"

func (cmd *trackRepository) exec(ctx context.Context, db *sql.DB, cfg config.Config) error {
	logger := cmd.logger.WithField("correlation_id", correlation.ExtractFromContext(ctx))

	req := trackRepositoryRequest{
		RelativePath:         cmd.relativePath,
		AuthoritativeStorage: cmd.authoritativeStorage,
		VirtualStorage:       cmd.virtualStorage,
	}

	return req.execRequest(ctx, db, cfg, cmd.w, logger, cmd.replicateImmediately)
}

func (req *trackRepositoryRequest) execRequest(ctx context.Context,
	db *sql.DB,
	cfg config.Config,
	w io.Writer,
	logger logrus.FieldLogger,
	replicateImmediately bool,
) error {
	logger.WithFields(logrus.Fields{
		"virtual_storage":       req.VirtualStorage,
		"relative_path":         req.RelativePath,
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

	authoritativeRepoExists, err := req.authoritativeRepositoryExists(ctx, cfg, w, primary)
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
				VirtualStorage:    req.VirtualStorage,
				SourceNodeStorage: primary,
				TargetNodeStorage: secondary,
			},
			Meta: datastore.Params{metadatahandler.CorrelationIDKey: correlationID},
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
		if errors.Is(err, commonerr.ErrRepositoryAlreadyExists) {
			fmt.Fprintf(w, "repository is already tracked in praefect database")
			return 0, nil
		}

		return 0, fmt.Errorf("ReserveRepositoryID: %w", err)
	}

	if err := ds.CreateRepository(
		ctx,
		repositoryID,
		req.VirtualStorage,
		req.RelativePath,
		req.RelativePath,
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

func (req *trackRepositoryRequest) authoritativeRepositoryExists(ctx context.Context, cfg config.Config, w io.Writer, nodeName string) (bool, error) {
	for _, vs := range cfg.VirtualStorages {
		if vs.Name != req.VirtualStorage {
			continue
		}

		for _, node := range vs.Nodes {
			if node.Storage == nodeName {
				logger.Debugf("check if repository %q exists on gitaly %q at %q", req.RelativePath, node.Storage, node.Address)
				repo := &gitalypb.Repository{
					StorageName:  node.Storage,
					RelativePath: req.RelativePath,
				}
				exists, err := repositoryExists(ctx, repo, node.Address, node.Token)
				if err != nil {
					fmt.Fprintf(w, "checking if repository exists %q, %q", node.Storage, req.RelativePath)
					return false, nil
				}
				return exists, nil
			}
		}
		return false, fmt.Errorf("node %q not found", req.AuthoritativeStorage)
	}
	return false, fmt.Errorf("virtual storage %q not found", req.VirtualStorage)
}
