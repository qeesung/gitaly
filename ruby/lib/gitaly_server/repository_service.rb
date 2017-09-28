module GitalyServer
  class RepositoryService < Gitaly::RepositoryService::Service
    def create_repository(request, _call)
      repo_path = GitalyServer.repo_path(_call)

      # TODO refactor Repository.create to eliminate bogus '/' argument
      Gitlab::Git::Repository.create('/', repo_path, bare: true, symlink_hooks_to: Gitlab.config.gitlab_shell.hooks_path)

      Gitaly::CreateRepositoryResponse.new
    end
  end
end
