module GitalyServer
  class RepositoryService < Gitaly::RepositoryService::Service
    include Utils

    def create_repository(request, call)
      bridge_exceptions do
        repo_path = GitalyServer.repo_path(call)

        Gitlab::Git::Repository.create(repo_path, bare: true, symlink_hooks_to: Gitlab.config.gitlab_shell.hooks_path)

        Gitaly::CreateRepositoryResponse.new
      end
    end

    def has_local_branches(request, call) # rubocop:disable Naming/PredicateName
      repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)

      Gitaly::HasLocalBranchesResponse.new(value: repo.has_local_branches?)
    end

    def fetch_source_branch(request, call)
      bridge_exceptions do
        source_repository = Gitlab::Git::GitalyRemoteRepository.new(request.source_repository, call)
        repository = Gitlab::Git::Repository.from_gitaly(request.repository, call)
        result = repository.fetch_source_branch!(source_repository, request.source_branch, request.target_ref)

        Gitaly::FetchSourceBranchResponse.new(result: result)
      end
    end
  end
end
