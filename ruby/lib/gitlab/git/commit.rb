module Gitlab
  module Git
    class Commit
      class << self
        def find(repo, commit_id = "HEAD")
          # Already a commit?
          return commit_id if commit_id.is_a?(Gitlab::Git::Commit)

          # A rugged reference?
          commit_id = Gitlab::Git::Ref.dereference_object(commit_id)
          return decorate(repo, commit_id) if commit_id.is_a?(Rugged::Commit)

          # Some weird thing?
          return nil unless commit_id.is_a?(String)

          # This saves us an RPC round trip.
          return nil if commit_id.include?(':')

          commit = rugged_find(repo, commit_id)

          decorate(repo, commit) if commit
        rescue Rugged::ReferenceError, Rugged::InvalidError, Rugged::ObjectError,
               Gitlab::Git::CommandError, Gitlab::Git::Repository::NoRepository,
               Rugged::OdbError, Rugged::TreeError, ArgumentError
          nil
        end

        def rugged_find(repo, commit_id)
          obj = repo.rev_parse_target(commit_id)

          obj.is_a?(Rugged::Commit) ? obj : nil
        end

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
