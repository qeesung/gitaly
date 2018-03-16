# External dependencies of Gitlab::Git
require 'rugged'
require 'linguist/blob_helper'
require 'securerandom'

# Ruby on Rails mix-ins that GitLab::Git code relies on
require 'active_support/core_ext/object/blank'
require 'active_support/core_ext/numeric/bytes'
require 'active_support/core_ext/numeric/time'
require 'active_support/core_ext/integer/time'
require 'active_support/core_ext/module/delegation'
require 'active_support/core_ext/hash/transform_values'
require 'active_support/core_ext/enumerable'

# We split our mock implementation of Gitlab::GitalyClient into a separate file
require_relative 'gitaly_client.rb'
require_relative 'git_logger.rb'
require_relative 'rails_logger.rb'
require_relative 'gollum.rb'
require_relative 'config.rb'

vendor_gitlab_git = '../../vendor/gitlab_git/'

# Some later requires are order-sensitive. Manually require whatever we need.
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/encoding_helper.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/utils/strong_memoize.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git/popen.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git/ref.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git/repository_mirroring.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git/storage/circuit_breaker_settings.rb')

# Require all .rb files we can find in the vendored gitlab/git directory
dir = File.expand_path(File.join('..', vendor_gitlab_git, 'lib/gitlab/'), __FILE__)
Dir["#{dir}/git/**/*.rb"].sort.each do |ruby_file|
  next if ruby_file.include?('circuit_breaker')

  require_relative ruby_file.sub(dir, File.join(vendor_gitlab_git, 'lib/gitlab/')).sub(%r{^/*}, '')
end

require_relative 'git/gitaly_remote_repository.rb'

module Gitlab
  module Git
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

        @absolute_object_directories = combined_alt_dirs
          .split(File::PATH_SEPARATOR)
          .map { |d| File.join(path, d) }

        @storage = gitaly_repository.storage_name
        @relative_path = gitaly_repository.relative_path
        @path = path
        @gl_repository = gl_repository
        @gitlab_projects = gitlab_projects
        @attributes = Gitlab::Git::InfoAttributes.new(path)
      end

      def circuit_breaker
        FakeCircuitBreaker
      end

      def gitaly_repository
        @gitaly_repository
      end

      def absolute_object_directories
        @absolute_object_directories
      end

      def relative_object_directories
        []
      end

      # This method was prematurely deleted from gitlab-ce. TODO: implement it in Go.
      def fsck
        msg, status = run_git(%W[--git-dir=#{path} fsck], nice: true)
        raise GitError.new("Could not fsck repository: #{msg}") unless status.zero?
      end
    end

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
    end
  end
end

class String
  # Because we are not rendering HTML, this is a no-op in gitaly-ruby.
  def html_safe
    self
  end
end

class FakeCircuitBreaker
  def self.perform
    yield
  end
end

class RequestStore
  def self.active?
    false
  end
end

module Gitlab
  module Git
    class Env
      NotAvailableInGitalyRuby = Class.new(StandardError)

      def self.all
        raise NotAvailableInGitalyRuby
      end
    end
  end
end

module Gitlab
  module GlId
    def self.gl_id(user)
      user.gl_id
    end
  end
end
