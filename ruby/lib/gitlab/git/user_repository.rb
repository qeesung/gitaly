module Gitlab
  module Git
    class UserRepository
      delegate :run_git, :run_git!, :with_worktree, :lookup, :find_branch,
               :worktree_path, :find_tag, :create_commit, :check_revert_content,
               :check_cherry_pick_content,
               to: :repository

      def initialize(repository, user)
        @repository = repository
        @user = user
      end

      def add_branch(branch_name, target:)
        target_object = Ref.dereference_object(lookup(target))
        raise Gitlab::Git::Repository::InvalidRef, "target not found: #{target}" unless target_object

        operation_service.add_branch(branch_name, target_object.oid)
        find_branch(branch_name)
      rescue Rugged::ReferenceError => ex
        raise Gitlab::Git::Repository::InvalidRef, ex
      end

      def add_tag(tag_name, target:, message: nil)
        target_object = Ref.dereference_object(lookup(target))
        raise Gitlab::Git::Repository::InvalidRef, "target not found: #{target}" unless target_object

        options = nil # Use nil, not the empty hash. Rugged cares about this.
        if message
          options = {
            message: message,
            tagger: Gitlab::Git.committer_hash(email: user.email, name: user.name)
          }
        end

        operation_service.add_tag(tag_name, target_object.oid, options)

        find_tag(tag_name)
      rescue Rugged::ReferenceError => ex
        raise Gitlab::Git::Repository::InvalidRef, ex
      rescue Rugged::TagError
        raise Gitlab::Git::Repository::TagExistsError
      end

      def update_branch(branch_name, newrev:, oldrev:)
        operation_service.update_branch(branch_name, newrev, oldrev)
      end

      def rm_branch(branch_name)
        branch = find_branch(branch_name)

        raise Gitlab::Git::Repository::InvalidRef, "branch not found: #{branch_name}" unless branch

        operation_service.rm_branch(branch)
      end

      def rm_tag(tag_name)
        tag = find_tag(tag_name)

        raise Gitlab::Git::Repository::InvalidRef, "tag not found: #{tag_name}" unless tag

        operation_service.rm_tag(tag)
      end

      def rebase(rebase_id, branch:, branch_sha:, remote_repository:, remote_branch:)
        rebase_path = worktree_path(Gitlab::Git::Repository::REBASE_WORKTREE_PREFIX, rebase_id)
        env = git_env

        if remote_repository.is_a?(RemoteRepository)
          env.merge!(remote_repository.fetch_env)
          remote_repo_path = Gitlab::Git::Repository::GITALY_INTERNAL_URL
        else
          remote_repo_path = remote_repository.path
        end

        with_worktree(rebase_path, branch, env: env) do
          run_git!(
            %W[pull --rebase #{remote_repo_path} #{remote_branch}],
            chdir: rebase_path, env: env
          )

          rebase_sha = run_git!(%w[rev-parse HEAD], chdir: rebase_path, env: env).strip

          update_branch(branch, newrev: rebase_sha, oldrev: branch_sha)

          rebase_sha
        end
      end

      def squash(squash_id, branch:, start_sha:, end_sha:, author:, message:)
        squash_path = repository.worktree_path(Gitlab::Git::Repository::SQUASH_WORKTREE_PREFIX, squash_id)
        env = git_env.merge(
          'GIT_AUTHOR_NAME' => author.name,
          'GIT_AUTHOR_EMAIL' => author.email
        )
        diff_range = "#{start_sha}...#{end_sha}"
        diff_files = repository.run_git!(
          %W[diff --name-only --diff-filter=ar --binary #{diff_range}]
        ).chomp

        with_worktree(squash_path, branch, sparse_checkout_files: diff_files, env: env) do
          # Apply diff of the `diff_range` to the worktree
          diff = run_git!(%W[diff --binary #{diff_range}])
          run_git!(%w[apply --index --whitespace=nowarn], chdir: squash_path, env: env) do |stdin|
            stdin.binmode
            stdin.write(diff)
          end

          # Commit the `diff_range` diff
          run_git!(%W[commit --no-verify --message #{message}], chdir: squash_path, env: env)

          # Return the squash sha. May print a warning for ambiguous refs, but
          # we can ignore that with `--quiet` and just take the SHA, if present.
          # HEAD here always refers to the current HEAD commit, even if there is
          # another ref called HEAD.
          run_git!(
            %w[rev-parse --quiet --verify HEAD], chdir: squash_path, env: env
          ).chomp
        end
      end

      def merge(source_sha, target_branch, message)
        committer = user_as_committer

        operation_service.with_branch(target_branch) do |start_commit|
          our_commit = start_commit.sha
          their_commit = source_sha

          raise 'Invalid merge target' unless our_commit
          raise 'Invalid merge source' unless their_commit

          merge_index = repository.rugged.merge_commits(our_commit, their_commit)
          break if merge_index.conflicts?

          options = {
            parents: [our_commit, their_commit],
            tree: merge_index.write_tree(repository.rugged),
            message: message,
            author: committer,
            committer: committer
          }

          commit_id = create_commit(options)

          yield commit_id

          commit_id
        end
      rescue Gitlab::Git::CommitError # when merge_index.conflicts?
        nil
      end

      def ff_merge(source_sha, target_branch)
        operation_service.with_branch(target_branch) do |our_commit|
          raise ArgumentError, 'Invalid merge target' unless our_commit

          source_sha
        end
      rescue Rugged::ReferenceError, Gitlab::Git::Repository::InvalidRef
        raise ArgumentError, 'Invalid merge source'
      end

      def revert(commit:, branch_name:, message:, start_branch_name:, start_repository:)
        operation_service.with_branch(
          branch_name,
          start_branch_name: start_branch_name,
          start_repository: start_repository
        ) do |start_commit|

          Gitlab::Git.check_namespace!(commit, start_repository)

          revert_tree_id = check_revert_content(commit, start_commit.sha)
          raise Gitlab::Git::Repository::CreateTreeError unless revert_tree_id

          create_commit(message: message,
                        author: user_as_committer,
                        committer: user_as_committer,
                        tree: revert_tree_id,
                        parents: [start_commit.sha])
        end
      end

      def cherry_pick(commit:, branch_name:, message:, start_branch_name:, start_repository:)
        operation_service.with_branch(
          branch_name,
          start_branch_name: start_branch_name,
          start_repository: start_repository
        ) do |start_commit|

          Gitlab::Git.check_namespace!(commit, start_repository)

          cherry_pick_tree_id = check_cherry_pick_content(commit, start_commit.sha)
          raise Gitlab::Git::Repository::CreateTreeError unless cherry_pick_tree_id

          committer = user_as_committer

          create_commit(message: message,
                        author: {
                          email: commit.author_email,
                          name: commit.author_name,
                          time: commit.authored_date
                        },
                        committer: committer,
                        tree: cherry_pick_tree_id,
                        parents: [start_commit.sha])
        end
      end

      def multi_action(branch_name:, message:, actions:,
                       author_email: nil, author_name: nil,
                       start_branch_name: nil, start_repository: repository)

        operation_service.with_branch(
          branch_name,
          start_branch_name: start_branch_name,
          start_repository: start_repository
        ) do |start_commit|

          index = Gitlab::Git::Index.new(repository)
          parents = []

          if start_commit
            index.read_tree(start_commit.rugged_commit.tree)
            parents = [start_commit.sha]
          end

          actions.each { |opts| index.apply(opts.delete(:action), opts) }

          committer = user_as_committer
          author = Gitlab::Git.committer_hash(email: author_email, name: author_name) || committer
          options = {
            tree: index.write_tree,
            message: message,
            parents: parents,
            author: author,
            committer: committer
          }

          create_commit(options)
        end
      end

      def commit_index(branch_name, index, options)
        committer = user_as_committer

        operation_service.with_branch(branch_name) do
          commit_params = options.merge(
            tree: index.write_tree(repository.rugged),
            author: committer,
            committer: committer
          )

          create_commit(commit_params)
        end
      end

      private

      attr_reader :user, :repository

      def operation_service
        @operation_service ||= OperationService.new(user, repository)
      end

      def git_env
        {
          'GIT_COMMITTER_NAME' => user.name,
          'GIT_COMMITTER_EMAIL' => user.email,
          'GL_ID' => Gitlab::GlId.gl_id(user),
          'GL_PROTOCOL' => Gitlab::Git::Hook::GL_PROTOCOL,
          'GL_REPOSITORY' => repository.gl_repository
        }
      end

      def user_as_committer
        Gitlab::Git.committer_hash(email: user.email, name: user.name)
      end
    end
  end
end
