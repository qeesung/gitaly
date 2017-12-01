# External dependencies of Gitlab::Git
require 'rugged'
require 'linguist/blob_helper'
require 'securerandom'

# Ruby on Rails mix-ins that GitLab::Git code relies on
require 'active_support/core_ext/object/blank'
require 'active_support/core_ext/numeric/bytes'
require 'active_support/core_ext/numeric/time'
require 'active_support/core_ext/module/delegation'
require 'active_support/core_ext/enumerable'

# We split our mock implementation of Gitlab::GitalyClient into a separate file
require_relative 'gitaly_client.rb'
require_relative 'git_logger.rb'
require_relative 'rails_logger.rb'
require_relative 'gollum.rb'

vendor_gitlab_git = '../../vendor/gitlab_git/'

# Some later requires are order-sensitive. Manually require whatever we need.
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/encoding_helper.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git/popen.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git/ref.rb')
require_relative File.join(vendor_gitlab_git, 'lib/gitlab/git/repository_mirroring.rb')

# Require all .rb files we can find in the vendored gitlab/git directory
dir = File.expand_path(File.join('..', vendor_gitlab_git, 'lib/gitlab/'), __FILE__)
Dir["#{dir}/git/**/*.rb"].sort.each do |ruby_file|
  next if ruby_file.include?('circuit_breaker')

  require_relative ruby_file.sub(dir, File.join(vendor_gitlab_git, 'lib/gitlab/')).sub(%r{^/*}, '')
end

require_relative 'git/gitaly_remote_repository.rb'

module Gitlab
  # Config lets Gitlab::Git do mock config lookups.
  class Config
    class Git
      def bin_path
        ENV['GITALY_RUBY_GIT_BIN_PATH']
      end

      def write_buffer_size
        @write_buffer_size ||= ENV['GITALY_RUBY_WRITE_BUFFER_SIZE'].to_i
      end
    end

    class GitlabShell
      def path
        ENV['GITALY_RUBY_GITLAB_SHELL_PATH']
      end

      def hooks_path
        File.join(path, 'hooks')
      end
    end

    class Gitaly
      def client_path
        ENV['GITALY_RUBY_GITALY_BIN_DIR']
      end
    end

    def git
      Git.new
    end

    def gitlab_shell
      GitlabShell.new
    end

    def gitaly
      Gitaly.new
    end
  end

  def self.config
    Config.new
  end
end

module Gitlab
  module Git
    class Repository
      def self.from_gitaly(gitaly_repository, call)
        new(
          gitaly_repository,
          GitalyServer.repo_path(call),
          GitalyServer.gl_repository(call),
          GitalyServer.repo_alt_dirs(call)
        )
      end

      def initialize(gitaly_repository, path, gl_repository, combined_alt_dirs="")
        @gitaly_repository = gitaly_repository

        alt_dirs = combined_alt_dirs
          .split(File::PATH_SEPARATOR)
          .map { |d| File.join(path, d) }

        @storage = gitaly_repository.storage_name
        @relative_path = gitaly_repository.relative_path
        @path = path
        @gl_repository = gl_repository
        @rugged = Rugged::Repository.new(path, alternates: alt_dirs)
        @attributes = Gitlab::Git::Attributes.new(path)
      end

      # Bypass the CircuitBreaker class which needs Redis
      def rugged
        @rugged
      end

      def circuit_breaker
        FakeCircuitBreaker
      end

      def gitaly_repository
        @gitaly_repository
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
