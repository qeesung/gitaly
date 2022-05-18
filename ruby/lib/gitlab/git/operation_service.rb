module Gitlab
  module Git
    class OperationService
      include Gitlab::Git::Popen

      BranchUpdate = Struct.new(:newrev, :repo_created, :branch_created) do
        alias_method :repo_created?, :repo_created
        alias_method :branch_created?, :branch_created

        def self.from_gitaly(branch_update)
          return if branch_update.nil?

          new(
            branch_update.commit_id,
            branch_update.repo_created,
            branch_update.branch_created
          )
        end
      end

      attr_reader :user, :repository

      def initialize(user, new_repository)
        @user = user
        @repository = new_repository
      end

      # Execute the block with the tip commit referenced by the given branch.
      # The branch must exist before calling this function.
      def with_branch(branch_name, &block)
        if !repository.empty? && !repository.branch_exists?(branch_name)
          raise ArgumentError, "Cannot find branch '#{branch_name}'"
        end

        update_branch_with_hooks(branch_name) do
          repository.with_repo_branch_commit(branch_name, &block)
        end
      end

      private

      # Returns [newrev, should_run_after_create, should_run_after_create_branch]
      def update_branch_with_hooks(branch_name)
        was_empty = repository.empty?

        # Make commit
        newrev = yield

        raise Gitlab::Git::CommitError.new('Failed to create commit') unless newrev

        branch = repository.find_branch(branch_name)
        oldrev = find_oldrev_from_branch(newrev, branch)

        ref = Gitlab::Git::BRANCH_REF_PREFIX + branch_name
        update_ref_in_hooks(ref, newrev, oldrev)

        BranchUpdate.new(newrev, was_empty, was_empty || Gitlab::Git.blank_ref?(oldrev))
      end

      def find_oldrev_from_branch(newrev, branch)
        return Gitlab::Git::BLANK_SHA unless branch

        oldrev = branch.target

        merge_base = repository.merge_base(newrev, branch.target)
        raise Gitlab::Git::Repository::InvalidRef unless merge_base

        if oldrev == merge_base
          oldrev
        else
          raise Gitlab::Git::CommitError.new('Branch diverged')
        end
      end

      def update_ref_in_hooks(ref, newrev, oldrev, push_options: nil, transaction: nil)
        with_hooks(ref, newrev, oldrev, push_options: push_options, transaction: transaction) do
          update_ref(ref, newrev, oldrev)
        end
      end

      def with_hooks(ref, newrev, oldrev, push_options: nil, transaction: nil)
        Gitlab::Git::HooksService.new.execute(
          user,
          repository,
          oldrev,
          newrev,
          ref,
          push_options: push_options,
          transaction: transaction
        ) do |service|
          yield(service)
        end
      end

      def update_ref(ref, newrev, oldrev)
        # We use 'git update-ref' because libgit2/rugged currently does not
        # offer 'compare and swap' ref updates. Without compare-and-swap we can
        # (and have!) accidentally reset the ref to an earlier state, clobbering
        # commits. See also https://github.com/libgit2/libgit2/issues/1534.
        command = %W[#{Gitlab.config.git.bin_path} update-ref --stdin -z]

        output, status = popen(
          command,
          repository.path
        ) do |stdin|
          stdin.write("update #{ref}\x00#{newrev}\x00#{oldrev}\x00")
        end

        unless status.zero?
          Gitlab::GitLogger.error("'git update-ref' in #{repository.path}: #{output}")
          ref_name = Gitlab::Git.branch_name(ref) || ref

          raise Gitlab::Git::CommitError.new(
            "Could not update #{ref_name}." \
            " Please refresh and try again."
          )
        end
      end
    end
  end
end
