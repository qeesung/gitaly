module Gitlab
  module Git
    class Commit
      class << self
        def shas_with_signatures(repository, shas)
          shas.select do |sha|
            begin
              Rugged::Commit.extract_signature(repository.rugged, sha)
            rescue Rugged::OdbError
              false
            end
          end
        end
      end

      def to_diff
        rugged_diff_from_parent.patch
      end
    end
  end
end
