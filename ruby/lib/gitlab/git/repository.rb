module Gitlab
  module Git
    # These are monkey patches on top of the vendored version of Repository.
    class Repository
      def self.from_gitaly(gitaly_repository, call)
        new(
          gitaly_repository,
          GitalyServer.repo_path(call),
          GitalyServer.gl_repository(call),
          Gitlab::Git::GitlabProjects.from_gitaly(gitaly_repository, call),
          GitalyServer.repo_alt_dirs(call)
        )
      end

      attr_reader :path

      def initialize(gitaly_repository, path, gl_repository, gitlab_projects, combined_alt_dirs="")
        @gitaly_repository = gitaly_repository

        @alternate_object_directories = combined_alt_dirs
          .split(File::PATH_SEPARATOR)
          .map { |d| File.join(path, d) }

        @storage = gitaly_repository.storage_name
        @relative_path = gitaly_repository.relative_path
        @path = path
        @gl_repository = gl_repository
        @gitlab_projects = gitlab_projects
      end

      def circuit_breaker
        FakeCircuitBreaker
      end

      def gitaly_repository
        @gitaly_repository
      end

      def alternate_object_directories
        @alternate_object_directories
      end

      def relative_object_directories
        raise "don't use relative object directories in gitaly-ruby"
      end

      # This method is mandatory and no longer exists in gitlab-ce.
      # TODO: implement it in Go because it is slow, and gitaly-ruby gets restarted a lot.
      def fsck
        msg, status = run_git(%W[--git-dir=#{path} fsck], nice: true)
        raise GitError.new("Could not fsck repository: #{msg}") unless status.zero?
      end
    end
  end
end

