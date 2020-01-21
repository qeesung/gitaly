module GitalyServer
  class BlobService < Gitaly::BlobService::Service
    include Utils

    # LFS pointers are maximum 200 bytes in size, and for a default message size
    # of 4 MB we got more than enough room for 100 blobs.
    MAX_LFS_POINTERS_PER_MESSSAGE = 100

    def get_lfs_pointers(request, call)
      repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)
      blobs = Gitlab::Git::Blob.batch_lfs_pointers(repo, request.blob_ids)

      Enumerator.new do |y|
        sliced_gitaly_lfs_pointers(blobs) do |lfs_pointers|
          y.yield Gitaly::GetLFSPointersResponse.new(lfs_pointers: lfs_pointers)
        end
      end
    end

    def get_new_lfs_pointers(request, call)
      Enumerator.new do |y|
        changes = lfs_changes(request, call)
        object_limit = request.limit.zero? ? nil : request.limit
        not_in = request.not_in_all ? :all : request.not_in_refs.to_a
        blobs = changes.new_pointers(object_limit: object_limit, not_in: not_in)

        sliced_gitaly_lfs_pointers(blobs) do |lfs_pointers|
          y.yield Gitaly::GetNewLFSPointersResponse.new(lfs_pointers: lfs_pointers)
        end
      end
    end

    def get_all_lfs_pointers(request, call)
      Enumerator.new do |y|
        repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)

        changes = Gitlab::Git::LfsChanges.new(repo, "")

        sliced_gitaly_lfs_pointers(changes.all_pointers) do |lfs_pointers|
          y.yield Gitaly::GetAllLFSPointersResponse.new(lfs_pointers: lfs_pointers)
        end
      end
    end

    private

    def lfs_changes(request, call)
      repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)

      Gitlab::Git::LfsChanges.new(repo, request.revision)
    end

    def sliced_gitaly_lfs_pointers(blobs)
      blobs.each_slice(MAX_LFS_POINTERS_PER_MESSSAGE) do |blobs_slice|
        yield (blobs_slice.map do |blob|
          Gitaly::LFSPointer.new(size: blob.size, data: blob.data, oid: blob.id)
        end)
      end
    end
  end
end
