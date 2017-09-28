module GitalyServer
  class RefService < Gitaly::RefService::Service
    include Utils

    TAGS_PER_MESSAGE = 100

    def create_branch(request, call)
      start_point = request.start_point
      start_point = 'HEAD' if start_point.empty?
      branch_name = request.name

      repo = Gitlab::Git::Repository.from_call(call)
      rugged_ref = repo.rugged.branches.create(branch_name, start_point)

      Gitaly::CreateBranchResponse.new(
        status: :OK,
        branch: Gitaly::Branch.new(
          name: rugged_ref.name.b,
          target_commit: gitaly_commit_from_rugged(rugged_ref.target)
        )
      )
    rescue Rugged::ReferenceError => e
      status = case e.to_s
               when /'refs\/heads\/#{branch_name}' is not valid/
                 :ERR_INVALID
               when /a reference with that name already exists/
                 :ERR_EXISTS
               else
                 :ERR_INVALID_START_POINT
               end

      Gitaly::CreateBranchResponse.new(status: status)
    end

    def delete_branch(request, call)
      branch_name = request.name
      raise GRPC::InvalidArgument.new("empty Name") if branch_name.empty?

      repo = Gitlab::Git::Repository.from_call(call)
      repo.delete_branch(branch_name)

      Gitaly::DeleteBranchResponse.new
    rescue Gitlab::Git::Repository::DeleteBranchError => e
      raise GRPC::Internal.new(e.to_s)
    end

    def find_branch(request, call)
      branch_name = request.name
      raise GRPC::InvalidArgument.new("empty Name") if branch_name.empty?

      repo = Gitlab::Git::Repository.from_call(call)
      rugged_branch = repo.find_branch(branch_name)
      gitaly_branch = Gitaly::Branch.new(
        name: rugged_branch.name.b,
        target_commit: gitaly_commit_from_rugged(rugged_branch.dereferenced_target.raw_commit)
      ) unless rugged_branch.nil?

      Gitaly::FindBranchResponse.new(branch: gitaly_branch)
    end

    def find_all_tags(request, call)
      repo = Gitlab::Git::Repository.from_call(call)

      Enumerator.new do |y|
        repo.tags.each_slice(TAGS_PER_MESSAGE) do |gitlab_tags|
          tags = gitlab_tags.map do |gitlab_tag|
            rugged_commit = gitlab_tag.dereferenced_target&.raw_commit
            gitaly_commit = gitaly_commit_from_rugged(rugged_commit) if rugged_commit

            Gitaly::Tag.new(
              name: gitlab_tag.name.b,
              id: gitlab_tag.target,
              message: gitlab_tag.message.to_s.b,
              target_commit: gitaly_commit
            )
          end

          y.yield Gitaly::FindAllTagsResponse.new(tags: tags)
        end
      end
    end
  end
end
