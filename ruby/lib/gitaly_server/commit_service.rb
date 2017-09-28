module GitalyServer
  class CommitService < Gitaly::CommitService::Service
    include Utils

    def commit_stats(request, call)
      repo = Gitlab::Git::Repository.from_call(call)
      revision = request.revision unless request.revision.empty?

      commit = Gitlab::Git::Commit.find(repo, revision)

      # In the odd case that the revision given doesn't exist we need to raise
      # an exception. Since GitLab (currently) already does this for us we don't
      # expect this to actually happen, just guarding against future code change
      raise GRPC::Internal.new("commit not found for revision '#{revision}'") unless commit

      stats = Gitlab::Git::CommitStats.new(repo, commit)

      Gitaly::CommitStatsResponse.new(oid: stats.id, additions: stats.additions, deletions: stats.deletions)
    end

    def find_commits(request, call)
      repository = Gitlab::Git::Repository.from_call(call)
      options = {
        ref: request.revision,
        limit: request.limit,
        follow: request.follow,
        skip_merges: request.skip_merges,
        disable_walk: request.disable_walk,
        offset: request.offset
      }
      options[:path] = request.paths unless request.paths.empty?

      options[:before] = Time.at(request.before.seconds).to_datetime if request.before
      options[:after] = Time.at(request.after.seconds).to_datetime if request.after

      Enumerator.new do |y|
        # Send back 'pages' with 20 commits each
        repository.raw_log(options).each_slice(20) do |rugged_commits|
          commits = rugged_commits.map do |rugged_commit|
            gitaly_commit_from_rugged(rugged_commit)
          end
          y.yield Gitaly::FindCommitsResponse.new(commits: commits)
        end
      end
    end
  end
end
