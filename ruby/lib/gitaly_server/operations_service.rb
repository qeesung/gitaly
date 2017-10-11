module GitalyServer
  class OperationsService < Gitaly::OperationService::Service
    include Utils

    def user_create_tag(request, call)
      bridge_exceptions do
        begin
          repo = Gitlab::Git::Repository.from_call(call)

          gitaly_user = request.user
          raise GRPC::InvalidArgument.new('empty user') unless gitaly_user
          user = Gitlab::Git::User.from_gitaly(gitaly_user)

          tag_name = request.tag_name
          raise GRPC::InvalidArgument.new('empty tag name') unless tag_name.present?

          target_revision = request.target_revision
          raise GRPC::InvalidArgument.new('empty target revision') unless target_revision.present?

          created_tag = repo.add_tag(tag_name, user: user, target: target_revision, message: request.message.presence)
          return Gitaly::UserCreateTagResponse.new unless created_tag

          rugged_commit = created_tag.dereferenced_target.rugged_commit
          commit = gitaly_commit_from_rugged(rugged_commit)
          tag = Gitaly::Tag.new(
            name: tag_name.b,
            id: created_tag.target,
            target_commit: commit,
            message: created_tag.message.to_s.b
          )

          Gitaly::UserCreateTagResponse.new(tag: tag)
        rescue Gitlab::Git::Repository::InvalidRef => e
          raise GRPC::FailedPrecondition.new(e.message)
        rescue Gitlab::Git::Repository::TagExistsError
          return Gitaly::UserCreateTagResponse.new(exists: true)
        rescue Gitlab::Git::HooksService::PreReceiveError => e
          return Gitaly::UserCreateTagResponse.new(pre_receive_error: e.message)
        end
      end
    end

    def user_delete_tag(request, call)
      bridge_exceptions do
        begin
          repo = Gitlab::Git::Repository.from_call(call)

          gitaly_user = request.user
          raise GRPC::InvalidArgument.new('empty user') unless gitaly_user
          user = Gitlab::Git::User.from_gitaly(gitaly_user)

          tag_name = request.tag_name
          raise GRPC::InvalidArgument.new('empty tag name') if tag_name.blank?

          repo.rm_tag(tag_name, user: user)

          Gitaly::UserDeleteTagResponse.new
        rescue Gitlab::Git::HooksService::PreReceiveError => e
          Gitaly::UserDeleteTagResponse.new(pre_receive_error: e.message)
        end
      end
    end

    def user_create_branch(request, call)
      bridge_exceptions do
        begin
          repo = Gitlab::Git::Repository.from_call(call)
          target = request.start_point
          raise GRPC::InvalidArgument.new('empty start_point') if target.empty?
          gitaly_user = request.user
          raise GRPC::InvalidArgument.new('empty user') unless gitaly_user

          branch_name = request.branch_name
          user = Gitlab::Git::User.from_gitaly(gitaly_user)
          created_branch = repo.add_branch(branch_name, user: user, target: target)
          return Gitaly::UserCreateBranchResponse.new unless created_branch

          rugged_commit = created_branch.dereferenced_target.rugged_commit
          commit = gitaly_commit_from_rugged(rugged_commit)
          branch = Gitaly::Branch.new(name: branch_name, target_commit: commit)
          Gitaly::UserCreateBranchResponse.new(branch: branch)
        rescue Gitlab::Git::Repository::InvalidRef, Gitlab::Git::CommitError => ex
          raise GRPC::FailedPrecondition.new(ex.message)
        rescue Gitlab::Git::HooksService::PreReceiveError => ex
          return Gitaly::UserCreateBranchResponse.new(pre_receive_error: ex.message)
        end
      end
    end

    def user_delete_branch(request, call)
      bridge_exceptions do
        begin
          repo = Gitlab::Git::Repository.from_call(call)
          user = Gitlab::Git::User.from_gitaly(request.user)

          repo.rm_branch(request.branch_name, user: user)

          Gitaly::UserDeleteBranchResponse.new
        rescue Gitlab::Git::HooksService::PreReceiveError => e
          Gitaly::UserDeleteBranchResponse.new(pre_receive_error: e.message)
        end
      end
    end

    def user_merge_branch(session, call)
      Enumerator.new do |y|
        bridge_exceptions do
          repository = Gitlab::Git::Repository.from_call(call)

          first_request = session.next
          user = Gitlab::Git::User.from_gitaly(first_request.user)
          source_sha = first_request.commit_id.dup
          target_branch = first_request.branch.dup
          message = first_request.message.dup

          merge_commit_id = nil
          repository.merge(user, source_sha, target_branch, message) do |commit_id|
            merge_commit_id = commit_id
            y << Gitaly::UserMergeBranchResponse.new(commit_id: commit_id)

            second_request = session.next
            unless second_request.apply
              raise GRPC::FailedPrecondition, 'merge aborted by client'
            end
          end

          y << Gitaly::UserMergeBranchResponse.new(branch_update: Gitaly::OperationBranchUpdate.new(commit_id: merge_commit_id))
        end
      end
    end
  end
end
