module GitalyServer
  class RemoteService < Gitaly::RemoteService::Service
    include Utils

    def add_remote(request, call)
      bridge_exceptions do
        repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)

        mirror_refmap = parse_refmaps(request.mirror_refmaps)

        repo.add_remote(request.name, request.url, mirror_refmap: mirror_refmap)

        Gitaly::AddRemoteResponse.new
      end
    end

    def remove_remote(request, call)
      bridge_exceptions do
        repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)

        result = repo.remove_remote(request.name)

        Gitaly::RemoveRemoteResponse.new(result: result)
      end
    end

    def fetch_internal_remote(request, call)
      bridge_exceptions do
        repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)
        remote_repo = Gitlab::Git::GitalyRemoteRepository.new(request.remote_repository, call)

        result = repo.fetch_repository_as_mirror(remote_repo)

        Gitaly::FetchInternalRemoteResponse.new(result: result)
      end
    end

    def find_remote_root_ref(request, call)
      bridge_exceptions do
        raise GRPC::InvalidArgument, "empty remote can't be queried" if request.remote.empty?

        gitlab_projects = Gitlab::Git::GitlabProjects.from_gitaly(request.repository, call)
        ref = gitlab_projects.find_remote_root_ref(request.remote, ssh_key: request.credentials&.ssh_key, known_hosts: request.credentials&.known_hosts)

        raise GRPC::Internal, "remote root ref not found for remote '#{request.remote}'" unless ref

        Gitaly::FindRemoteRootRefResponse.new(ref: ref)
      end
    end

    def update_remote_mirror(call)
      bridge_exceptions do
        request_enum = call.each_remote_read
        first_request = request_enum.next
        repo = Gitlab::Git::Repository.from_gitaly(first_request.repository, call)
        only_branches_matching = first_request.only_branches_matching.to_a

        only_branches_matching += request_enum.flat_map(&:only_branches_matching)

        remote_mirror = Gitlab::Git::RemoteMirror.new(repo, first_request.ref_name)
        remote_mirror.update(only_branches_matching: only_branches_matching)

        Gitaly::UpdateRemoteMirrorResponse.new
      end
    end

    private

    def parse_refmaps(refmaps)
      return unless refmaps.present?

      parsed_refmaps = refmaps.map do |refmap|
        next unless refmap.present?

        refmap_spec = refmap.to_sym

        if Gitlab::Git::RepositoryMirroring::REFMAPS.has_key?(refmap_spec)
          refmap_spec
        else
          refmap
        end
      end

      parsed_refmaps.compact.presence
    end
  end
end
