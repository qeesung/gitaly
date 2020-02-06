module GitalyServer
  class ConflictsService < Gitaly::ConflictsService::Service
    include Utils

    def list_conflict_files(request, call)
      repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)
      resolver = Gitlab::Git::Conflict::Resolver.new(repo, request.our_commit_oid, request.their_commit_oid)
      conflicts = resolver.conflicts
      files = []
      msg_size = 0

      Enumerator.new do |y|
        enumerate_conflicts(conflicts) do |file|
          files << Gitaly::ConflictFile.new(header: conflict_file_header(file))

          strio = StringIO.new(file.content)
          while chunk = strio.read(Gitlab.config.git.write_buffer_size - msg_size)
            files << Gitaly::ConflictFile.new(content: chunk)
            msg_size += chunk.bytesize

            # We don't send a message for each chunk because the content of
            # a file may be smaller than the size limit, which means we can
            # keep adding data to the message
            next if msg_size < Gitlab.config.git.write_buffer_size

            y.yield(Gitaly::ListConflictFilesResponse.new(files: files))

            files = []
            msg_size = 0
          end
        end

        # Send leftover data, if any
        y.yield(Gitaly::ListConflictFilesResponse.new(files: files)) if files.any?
      end
    rescue Gitlab::Git::Conflict::Resolver::ListError => e
      raise GRPC::FailedPrecondition.new(e.message)
    end

    def resolve_conflicts(call)
      header = nil
      files_json = ""

      call.each_remote_read.each_with_index do |request, index|
        if index.zero?
          header = request.header
        else
          files_json << request.files_json
        end
      end

      repo = Gitlab::Git::Repository.from_gitaly(header.repository, call)
      remote_repo = Gitlab::Git::GitalyRemoteRepository.new(header.target_repository, call)
      resolver = Gitlab::Git::Conflict::Resolver.new(remote_repo, header.our_commit_oid, header.their_commit_oid)
      user = Gitlab::Git::User.from_gitaly(header.user)
      files = JSON.parse(files_json).map(&:with_indifferent_access)

      begin
        resolution = Gitlab::Git::Conflict::Resolution.new(user, files, header.commit_message.dup)
        params = {
          source_branch: header.source_branch,
          target_branch: header.target_branch
        }
        resolver.resolve_conflicts(repo, resolution, params)

        Gitaly::ResolveConflictsResponse.new
      rescue Gitlab::Git::Conflict::Resolver::ResolutionError => e
        Gitaly::ResolveConflictsResponse.new(resolution_error: e.message)
      end
    end

    private

    def conflict_file_header(file)
      Gitaly::ConflictFileHeader.new(
        commit_oid: file.commit_oid,
        their_path: file.their_path.b,
        our_path: file.our_path.b,
        our_mode: file.our_mode
      )
    end

    def enumerate_conflicts(conflicts)
      conflicts.each do |file|
        yield file
      end
    rescue Gitlab::Git::Conflict::File::UnsupportedEncoding, Gitlab::Git::Conflict::Resolver::ListError => e
      raise GRPC::FailedPrecondition.new(e.message)
    end
  end
end
