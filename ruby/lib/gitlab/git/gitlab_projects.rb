require 'tempfile'

module Gitlab
  module Git
    class GitlabProjects
      include Gitlab::Git::Popen
      include Gitlab::Utils::StrongMemoize

      HEAD_PREFIX = 'HEAD branch: '.freeze

      # Relative path is a directory name for repository with .git at the end.
      # Example: gitlab-org/gitlab-test.git
      attr_reader :repository_relative_path

      # This is the path at which the gitlab-shell hooks directory can be found.
      # It's essential for integration between git and GitLab proper. All new
      # repositories should have their hooks directory symlinked here.
      attr_reader :global_hooks_path

      attr_reader :logger

      def self.from_gitaly(gitaly_repository, call)
        storage_path = GitalyServer.storage_path(call)

        Gitlab::Git::GitlabProjects.new(
          storage_path,
          gitaly_repository.relative_path,
          global_hooks_path: Gitlab::Git::Hook.directory,
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

      def output
        io = @output.dup
        io.rewind
        io.read
      end

      # Absolute path to the repository.
      # Example: /home/git/repositorities/gitlab-org/gitlab-test.git
      # Probably will be removed when we fully migrate to Gitaly, part of
      # https://gitlab.com/gitlab-org/gitaly/issues/1124.
      def repository_absolute_path
        strong_memoize(:repository_absolute_path) do
          File.join(shard_path, repository_relative_path)
        end
      end

      def fetch_remote(name, timeout, force:, tags:, ssh_key: nil, known_hosts: nil, prune: true)
        logger.info "Fetching remote #{name} for repository #{repository_absolute_path}."
        cmd = fetch_remote_command(name, tags, prune, force)

        setup_ssh_auth(ssh_key, known_hosts) do |env|
          run_with_timeout(cmd, timeout, repository_absolute_path, env).tap do |success|
            logger.error "Fetching remote #{name} for repository #{repository_absolute_path} failed." unless success
          end
        end
      end

      def find_remote_root_ref(remote_name, ssh_key: nil, known_hosts: nil)
        logger.info "Finding remote root ref from #{remote_name} for repository #{repository_absolute_path}."

        cmd = %W(#{Gitlab.config.git.bin_path} remote show #{remote_name})
        ref = nil

        setup_ssh_auth(ssh_key, known_hosts) do |env|
          if run(cmd, repository_absolute_path, env)
            output.each_line do |line|
              line = line.strip

              if line.start_with?(HEAD_PREFIX)
                ref = line.sub(HEAD_PREFIX, '')
                break
              end
            end
          else
            logger.error("Finding remote root ref from #{remote_name} for repository #{repository_absolute_path} failed.")
          end
        end

        ref
      end

      def push_branches(remote_name, timeout, force, branch_names)
        logger.info "Pushing branches from #{repository_absolute_path} to remote #{remote_name}: #{branch_names}"
        cmd = %W(#{Gitlab.config.git.bin_path} push)
        cmd << '--force' if force
        cmd += %W(-- #{remote_name}).concat(branch_names)

        success = run_with_timeout(cmd, timeout, repository_absolute_path)

        logger.error("Pushing branches to remote #{remote_name} failed.") unless success

        success
      end

      def delete_remote_branches(remote_name, branch_names)
        branches = branch_names.map { |branch_name| ":#{branch_name}" }

        logger.info "Pushing deleted branches from #{repository_absolute_path} to remote #{remote_name}: #{branch_names}"
        cmd = %W(#{Gitlab.config.git.bin_path} push -- #{remote_name}).concat(branches)

        success = run(cmd, repository_absolute_path)

        logger.error("Pushing deleted branches to remote #{remote_name} failed.") unless success

        success
      end

      protected

      def run(*args)
        output, exitstatus = popen(*args)
        @output << output

        exitstatus&.zero?
      end

      def run_with_timeout(*args)
        output, exitstatus = popen_with_timeout(*args)
        @output << output

        exitstatus&.zero?
      rescue Timeout::Error
        @output.puts('Timed out')

        false
      end

      def mask_password_in_url(url)
        result = URI(url)
        result.password = "*****" unless result.password.nil?
        result.user = "*****" unless result.user.nil? # it's needed for oauth access_token
        result
      rescue
        url
      end

      def remove_origin_in_repo
        cmd = %W(#{Gitlab.config.git.bin_path} remote rm origin)
        run(cmd, repository_absolute_path)
      end

      # Builds a small shell script that can be used to execute SSH with a set of
      # custom options.
      #
      # Options are expanded as `'-oKey="Value"'`, so SSH will correctly interpret
      # paths with spaces in them. We trust the user not to embed single or double
      # quotes in the key or value.
      def custom_ssh_script(options = {})
        args = options.map { |k, v| %{'-o#{k}="#{v}"'} }.join(' ')

        [
          "#!/bin/sh",
          "exec ssh #{args} \"$@\""
        ].join("\n")
      end

      # Known hosts data and private keys can be passed to gitlab-shell in the
      # environment. If present, this method puts them into temporary files, writes
      # a script that can substitute as `ssh`, setting the options to respect those
      # files, and yields: { "GIT_SSH" => "/tmp/myScript" }
      def setup_ssh_auth(key, known_hosts)
        options = {}

        if key
          key_file = Tempfile.new('gitlab-shell-key-file')
          key_file.chmod(0o400)
          key_file.write(key)
          key_file.close

          options['IdentityFile'] = key_file.path
          options['IdentitiesOnly'] = 'yes'
        end

        if known_hosts
          known_hosts_file = Tempfile.new('gitlab-shell-known-hosts')
          known_hosts_file.chmod(0o400)
          known_hosts_file.write(known_hosts)
          known_hosts_file.close

          options['StrictHostKeyChecking'] = 'yes'
          options['UserKnownHostsFile'] = known_hosts_file.path
        end

        return yield({}) if options.empty?

        script = Tempfile.new('gitlab-shell-ssh-wrapper')
        script.chmod(0o755)
        script.write(custom_ssh_script(options))
        script.close

        yield('GIT_SSH' => script.path)
      ensure
        key_file&.close!
        known_hosts_file&.close!
        script&.close!
      end

      private

      def fetch_remote_command(name, tags, prune, force)
        %W(#{Gitlab.config.git.bin_path} fetch #{name} --quiet).tap do |cmd|
          cmd << '--prune' if prune
          cmd << '--force' if force
          cmd << (tags ? '--tags' : '--no-tags')
        end
      end
    end
  end
end
