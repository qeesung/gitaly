module Gitlab
  module Git
    class GitalyRemoteRepository < RemoteRepository
      CLIENT_NAME = 'gitaly-ruby'.freeze

      attr_reader :gitaly_client

      def initialize(gitaly_repository, call)
        @gitaly_repository = gitaly_repository
        @storage = gitaly_repository.storage_name
        @gitaly_client = GitalyServer.client(call)
      end

      def path
        raise 'gitaly-ruby cannot access remote repositories by path'
      end

      def empty_repo?
        !exists? || !has_visible_content?
      end

      def commit_id(revision)
        request = Gitaly::FindCommitRequest.new(repository: @gitaly_repository, revision: revision.b)
        stub = Gitaly::CommitService::Stub.new(address, credentials)
        stub.find_commit(request, request_kwargs)&.commit&.id.presence
      end

      private

      def exists?
        request = Gitaly::RepositoryExistsRequest.new(repository: @gitaly_repository)
        stub = Gitaly::RepositoryService::Stub.new(address, credentials)
        stub.repository_exists(request, request_kwargs).exists
      end

      def has_visible_content?
        request = Gitaly::HasLocalBranchesRequest.new(repository: @gitaly_repository)
        stub = Gitaly::RepositoryService::Stub.new(address, credentials)
        stub.has_local_branches(request, request_kwargs).value
      end

      def address
        gitaly_client.address(storage)
      end

      def credentials
        :this_channel_is_insecure
      end

      def token
        gitaly_client.token(storage)
      end

      def request_kwargs
        @request_kwargs ||= begin
          encoded_token = Base64.strict_encode64(token.to_s)
          metadata = {
            'authorization' => "Bearer #{encoded_token}",
            'client_name' => CLIENT_NAME
          }

          { metadata: metadata }
        end
      end
    end
  end
end
