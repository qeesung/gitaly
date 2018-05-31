module Gitlab
  module Git
    # These are monkey patches on top of the vendored version of Repository.
    class Repository
      class << self
        def from_gitaly(gitaly_repository, call)
          new(
            gitaly_repository,
            GitalyServer.repo_path(call),
            GitalyServer.gl_repository(call),
            Gitlab::Git::GitlabProjects.from_gitaly(gitaly_repository, call),
            GitalyServer.repo_alt_dirs(call)
          )
        end

        def create(repo_path)
          FileUtils.mkdir_p(repo_path, mode: 0770)

          # Equivalent to `git --git-path=#{repo_path} init [--bare]`
          repo = Rugged::Repository.init_at(repo_path, true)
          repo.close

          symlink_hooks_to = Gitlab.config.gitlab_shell.hooks_path
          create_hooks(repo_path, symlink_hooks_to) if symlink_hooks_to.present?
        end

        def create_hooks(repo_path, global_hooks_path)
          local_hooks_path = File.join(repo_path, 'hooks')
          real_local_hooks_path = :not_found

          begin
            real_local_hooks_path = File.realpath(local_hooks_path)
          rescue Errno::ENOENT
            # real_local_hooks_path == :not_found
          end

          # Do nothing if hooks already exist
          unless real_local_hooks_path == File.realpath(global_hooks_path)
            if File.exist?(local_hooks_path)
              # Move the existing hooks somewhere safe
              FileUtils.mv(
                local_hooks_path,
                "#{local_hooks_path}.old.#{Time.now.to_i}")
            end

            # Create the hooks symlink
            FileUtils.ln_sf(global_hooks_path, local_hooks_path)
          end

          true
        end
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

      def add_branch(branch_name, user:, target:)
        target_object = Ref.dereference_object(lookup(target))
        raise InvalidRef.new("target not found: #{target}") unless target_object

        OperationService.new(user, self).add_branch(branch_name, target_object.oid)
        find_branch(branch_name)
      rescue Rugged::ReferenceError => ex
        raise InvalidRef, ex
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
      # TODO: Can be removed once https://gitlab.com/gitlab-org/gitaly/merge_requests/738
      #       is well and truly out in the wild.
      def fsck
        msg, status = run_git(%W[--git-dir=#{path} fsck], nice: true)
        raise GitError.new("Could not fsck repository: #{msg}") unless status.zero?
      end

      def exists?
        File.exist?(File.join(path, 'refs'))
      end
    end
  end
end

