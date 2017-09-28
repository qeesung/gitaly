module GitalyServer
  class DiffService < Gitaly::DiffService::Service
    def commit_patch(request, call)
      repo = Gitlab::Git::Repository.from_call(call)
      commit = Gitlab::Git::Commit.find(repo, request.revision)

      Enumerator.new do |y|
        io = StringIO.new(commit.to_diff)
        while chunk = io.read(Gitlab.config.git.write_buffer_size)
          y.yield Gitaly::CommitPatchResponse.new(data: chunk)
        end
      end
    end
  end
end
