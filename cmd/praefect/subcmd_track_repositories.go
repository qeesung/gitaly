package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/labkit/correlation"
)

const (
	trackRepositoriesCmdName = "track-repositories"
	paramInputPath           = "input-path"
)

type invalidRequest struct {
	reqNum int
	path   string
	errs   []error
}

type dupPathError struct {
	path    string
	reqNums []int
}

func (d *dupPathError) Error() string {
	return fmt.Sprintf("duplicate entries for relative_path, item #: %v", d.reqNums)
}

type trackRepositories struct {
	w                    io.Writer
	logger               logrus.FieldLogger
	inputPath            string
	replicateImmediately bool
}

func newTrackRepositories(logger logrus.FieldLogger, w io.Writer) *trackRepositories {
	return &trackRepositories{w: w, logger: logger}
}

func (cmd *trackRepositories) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(trackRepositoryCmdName, flag.ExitOnError)
	fs.BoolVar(&cmd.replicateImmediately, "replicate-immediately", false, "kick off a replication immediately")
	fs.StringVar(&cmd.inputPath, paramInputPath, "", "path to file with details of repositories to track")
	fs.Usage = func() {
		printfErr("Description:\n" +
			"	This command allows bulk requests for repositories to be tracked by Praefect.\n" +
			"	The -input-path flag must be the path of a file containing the details of the repositories\n" +
			"	to track as a list of newline-delimited JSON objects. Each entry must contain the\n" +
			"	following keys:\n\n" +
			"		relative_path - The relative path of the repository on-disk.\n" +
			"		virtual_storage - The Praefect virtual storage name.\n" +
			"		authoritative_storage - Which storage to consider as the canonical copy of the repository.\n\n" +
			"	If -replicate-immediately is used, the command will attempt to replicate the repositories\n" +
			"	to the secondaries. Otherwise, replication jobs will be created and will be executed\n" +
			"	eventually by Praefect itself.\n")
		fs.PrintDefaults()
	}
	return fs
}

func (cmd trackRepositories) Exec(flags *flag.FlagSet, cfg config.Config) error {
	switch {
	case flags.NArg() > 0:
		return unexpectedPositionalArgsError{Command: flags.Name()}
	}

	ctx := correlation.ContextWithCorrelation(context.Background(), correlation.SafeRandomID())
	logger = cmd.logger.WithField("correlation_id", correlation.ExtractFromContext(ctx))

	openDBCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := glsql.OpenDB(openDBCtx, cfg.DB)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer func() { _ = db.Close() }()
	store := datastore.NewPostgresRepositoryStore(db, cfg.StorageNames())

	f, err := os.Open(cmd.inputPath)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer f.Close()

	d := json.NewDecoder(f)
	d.DisallowUnknownFields()

	fmt.Fprintf(cmd.w, "Validating repository information in %q\n", cmd.inputPath)

	var requests []trackRepositoryRequest
	var repoNum int
	var repoErrs []invalidRequest
	pathLines := make(map[string][]int)

	// Read in and validate all requests from input file before executing. This prevents us from
	// partially executing a file, which makes it difficult to tell which repos were actually
	// tracked.
	for d.More() {
		repoNum++

		request := trackRepositoryRequest{}
		badReq := invalidRequest{reqNum: repoNum}

		if err := d.Decode(&request); err != nil {
			badReq.errs = append(badReq.errs, err)
			repoErrs = append(repoErrs, badReq)

			// Invalid request, nothing to validate.
			continue
		}

		if request.RelativePath == "" {
			badReq.errs = append(badReq.errs, requiredParameterError(paramRelativePath))
		}
		badReq.path = request.RelativePath

		if request.VirtualStorage == "" {
			badReq.errs = append(badReq.errs, requiredParameterError(paramVirtualStorage))
		}
		if request.AuthoritativeStorage == "" {
			badReq.errs = append(badReq.errs, requiredParameterError(paramAuthoritativeStorage))
		}
		if len(badReq.errs) > 0 {
			repoErrs = append(repoErrs, badReq)

			// Incomplete request, no further validation possible.
			continue
		}

		// Repo paths are globally unique, any attempt to add the same path multiple virtual storages
		// is invalid and must be rejected.
		prevLines, exists := pathLines[request.RelativePath]
		if exists {
			badReq.errs = append(badReq.errs, &dupPathError{path: request.RelativePath})
			repoErrs = append(repoErrs, badReq)

			prevLines = append(prevLines, repoNum)
			pathLines[request.RelativePath] = prevLines

			// We've already checked this path, no need to run further checks.
			continue
		}
		pathLines[request.RelativePath] = []int{repoNum}

		repoInDB, err := store.RepositoryExists(ctx, request.VirtualStorage, request.RelativePath)
		if err != nil {
			// Bail out if we're having trouble contacting the DB, nothing is going to work if this fails.
			return fmt.Errorf("checking database: %w", err)
		}
		if repoInDB {
			badReq.errs = append(badReq.errs, fmt.Errorf("repository is already tracked by Praefect"))
			repoErrs = append(repoErrs, badReq)
			// Repo already in Praefect DB, we can skip it.
			continue
		}

		authoritativeRepoExists, err := request.authoritativeRepositoryExists(ctx, cfg, cmd.w, request.AuthoritativeStorage)
		if err != nil {
			badReq.errs = append(badReq.errs, fmt.Errorf("checking repository on disk: %w", err))
		} else if !authoritativeRepoExists {
			badReq.errs = append(badReq.errs, fmt.Errorf("not a valid git repository"))
		}

		if len(badReq.errs) > 0 {
			repoErrs = append(repoErrs, badReq)
			continue
		}
		requests = append(requests, request)
	}

	if len(repoErrs) > 0 {
		printInvalidRequests(cmd.w, repoErrs, pathLines, cmd.inputPath)
		return fmt.Errorf("invalid entries found, aborting")
	}
	if len(requests) == 0 {
		return fmt.Errorf("no repository information found in %q", cmd.inputPath)
	}

	fmt.Fprintf(cmd.w, "All repository details are correctly formatted\n")
	fmt.Fprintf(cmd.w, "Tracking %v repositories in Praefect DB...\n", repoNum)
	for _, request := range requests {
		if err := request.execRequest(ctx, db, cfg, cmd.w, logger, cmd.replicateImmediately); err != nil {
			return fmt.Errorf("tracking repository %q: %w", request.RelativePath, err)
		}
	}

	return nil
}

func printInvalidRequests(w io.Writer, repoErrs []invalidRequest, pathLines map[string][]int, inputPath string) {
	fmt.Fprintf(w, "Found %v invalid request(s) in %q:\n", len(repoErrs), inputPath)

	for _, l := range repoErrs {
		fmt.Fprintf(w, "  item #: %v, relative_path: %q\n", l.reqNum, l.path)
		for _, err := range l.errs {
			if dup, ok := err.(*dupPathError); ok {
				// The complete set of duplicate reqNums won't be known until input is
				// fully processed, fetch them now.
				err = &dupPathError{path: dup.path, reqNums: pathLines[dup.path]}
			}
			fmt.Fprintf(w, "    %v\n", err)
		}
	}
}
