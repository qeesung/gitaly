module Gitlab
  module Git
    class Commit
      def to_diff
        rugged_diff_from_parent.patch
      end
    end
  end
end
