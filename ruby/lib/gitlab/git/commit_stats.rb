# Gitlab::Git::CommitStats counts the additions, deletions, and total changes
# in a commit.
module Gitlab
  module Git
    class CommitStats
      attr_reader :id, :additions, :deletions, :total

      # Instantiate a CommitStats object
      #
      # Gitaly migration: https://gitlab.com/gitlab-org/gitaly/issues/323
      def initialize(repo, commit)
        @id = commit.id
        @additions = 0
        @deletions = 0
        @total = 0

        rugged_stats(commit)
      end

      def rugged_stats(commit)
        diff = commit.rugged_diff_from_parent
        _files_changed, @additions, @deletions = diff.stat
        @total = @additions + @deletions
      end
    end
  end
end
