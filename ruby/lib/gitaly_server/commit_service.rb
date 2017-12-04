module GitalyServer
  class CommitService < Gitaly::CommitService::Service
    include Utils

    def commit_stats(request, call)
      bridge_exceptions do
        repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)
        revision = request.revision unless request.revision.empty?

        commit = Gitlab::Git::Commit.find(repo, revision)

        # In the odd case that the revision given doesn't exist we need to raise
        # an exception. Since GitLab (currently) already does this for us we don't
        # expect this to actually happen, just guarding against future code change
        raise GRPC::Internal.new("commit not found for revision '#{revision}'") unless commit

        stats = Gitlab::Git::CommitStats.new(repo, commit)

        Gitaly::CommitStatsResponse.new(oid: stats.id, additions: stats.additions, deletions: stats.deletions)
      end
    end

    def list_commits_by_oid(request, call)
      bridge_exceptions do
        repository = Gitlab::Git::Repository.from_gitaly(request.repository, call)

        Enumerator.new do |y|
          request.oid.each_slice(20) do |oids|
            commits = oids.map do |oid|
              commit =
                begin
                  repository.rev_parse_target(oid)
                rescue Rugged::ReferenceError, Rugged::InvalidError
                  nil
                end

              commit.is_a?(Rugged::Commit) ? gitaly_commit_from_rugged(commit) : nil
            end.compact

            y.yield Gitaly::ListCommitsByOidResponse.new(commits: commits)
          end
        end
      end
    end

    def find_commits(request, call)
      bridge_exceptions do
        repository = Gitlab::Git::Repository.from_gitaly(request.repository, call)
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

    def filter_shas_with_signatures(session, call)
      Enumerator.new do |y|
        bridge_exceptions do
          repository = nil

          call.each_remote_read.with_index do |request, index|
            if index.zero?
              repository = Gitlab::Git::Repository.from_gitaly(request.repository, call)
            end

            y << Gitaly::FilterShasWithSignaturesResponse.new(shas: Gitlab::Git::Commit.shas_with_signatures(repository, request.shas))
          end
        end
      end
    end
  end
end
