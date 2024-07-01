package housekeeping

import (
	"bytes"
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// WriteCommitGraphConfigForRepository returns the optimal default-configuration for the given repo.
// By default, the configuration will ask for an incremental commit-graph update. If the preexisting
// commit-graph is missing bloom filters though then the whole commit-graph chain will be rewritten.
func WriteCommitGraphConfigForRepository(ctx context.Context, repo *localrepo.Repo) (config.WriteCommitGraphConfig, error) {
	repoPath, err := repo.Path(ctx)
	if err != nil {
		return config.WriteCommitGraphConfig{}, err
	}

	commitGraphInfo, err := stats.CommitGraphInfoForRepository(repoPath)
	if err != nil {
		return config.WriteCommitGraphConfig{}, structerr.NewInternal("getting commit-graph info: %w", err)
	}

	return config.WriteCommitGraphConfig{
		ReplaceChain: commitGraphNeedsRewrite(ctx, commitGraphInfo),
	}, nil
}

// commitGraphNeedsRewrite determines whether the commit-graph needs to be rewritten. This can be
// the case when it is either a monolithic commit-graph or when it is missing some extensions that
// only get written on a full rewrite.
func commitGraphNeedsRewrite(ctx context.Context, commitGraphInfo stats.CommitGraphInfo) bool {
	if commitGraphInfo.CommitGraphChainLength == 0 {
		// The repository does not have a commit-graph chain. This either indicates we ain't
		// got no commit-graph at all, or that it's monolithic. In both cases we want to
		// replace the commit-graph chain.
		return true
	} else if !commitGraphInfo.HasBloomFilters {
		// If the commit-graph-chain exists, we want to rewrite it in case we see that it
		// ain't got bloom filters enabled. This is because Git will refuse to write any
		// bloom filters as long as any of the commit-graph slices is missing this info.
		return true
	} else if !commitGraphInfo.HasGenerationData {
		// The same is true for generation data.
		return true
	}

	return false
}

// WriteCommitGraph updates the commit-graph in the given repository. The commit-graph is updated
// incrementally, except in the case where it doesn't exist yet or in case it is detected that the
// commit-graph is missing bloom filters.
func WriteCommitGraph(ctx context.Context, repo *localrepo.Repo, cfg config.WriteCommitGraphConfig) error {
	flags := []git.Option{
		git.Flag{Name: "--reachable"},
		git.Flag{Name: "--changed-paths"}, // enables Bloom filters
		git.ValueFlag{Name: "--size-multiple", Value: "4"},
	}

	if cfg.ReplaceChain {
		flags = append(flags, git.Flag{Name: "--split=replace"})
	} else {
		flags = append(flags, git.Flag{Name: "--split"})
	}

	var stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx, git.Command{
		Name:   "commit-graph",
		Action: "write",
		Flags:  flags,
	}, git.WithStderr(&stderr)); err != nil {
		return structerr.NewInternal("writing commit-graph: %w: %v", err, stderr.String())
	}

	return nil
}
