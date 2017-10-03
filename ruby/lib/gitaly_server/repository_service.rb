module GitalyServer
  class RepositoryService < Gitaly::RepositoryService::Service
    include Utils

    def create_repository(request, _call)
      bridge_exceptions do
        repo_path = GitalyServer.repo_path(_call)

        Gitlab::Git::Repository.create(repo_path, bare: true, symlink_hooks_to: Gitlab.config.gitlab_shell.hooks_path)

        Gitaly::CreateRepositoryResponse.new
      end
    end
  end
end
