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

      def git_timeout
        10800 # TODO make this configurable or eliminate otherwise https://gitlab.com/gitlab-org/gitaly/issues/885
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
