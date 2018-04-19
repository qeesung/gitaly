module Gitlab
  module Git
    # These are monkey patches on top of the vendored version of GitlabProjects.
    class GitlabProjects
      def self.from_gitaly(gitaly_repository, call)
        storage_path = GitalyServer.storage_path(call)

        Gitlab::Git::GitlabProjects.new(
          storage_path,
          gitaly_repository.relative_path,
          global_hooks_path: Gitlab.config.gitlab_shell.hooks_path,
          logger: Rails.logger
        )
      end

      def initialize(shard_path, repository_relative_path, global_hooks_path:, logger:)
        @shard_path = shard_path
        @repository_relative_path = repository_relative_path

        @logger = logger
        @global_hooks_path = global_hooks_path
        @output = StringIO.new
      end


      def shard_name
        raise "don't use shard_name in gitaly-ruby"
      end

      def shard_path
        @shard_path
      end
    end
  end
end

