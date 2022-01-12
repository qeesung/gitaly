# Generated by the protocol buffer compiler.  DO NOT EDIT!
# Source: commit.proto for package 'gitaly'

require 'grpc'
require 'commit_pb'

module Gitaly
  module CommitService
    class Service

      include ::GRPC::GenericService

      self.marshal_class_method = :encode
      self.unmarshal_class_method = :decode
      self.service_name = 'gitaly.CommitService'

      # ListCommits lists all commits reachable via a set of references by doing a
      # graph walk. This deprecates FindAllCommits and FindCommits (except Follow
      # is not yet supported). Any unknown revisions will cause the RPC to fail.
      rpc :ListCommits, ::Gitaly::ListCommitsRequest, stream(::Gitaly::ListCommitsResponse)
      # ListAllCommits lists all commits present in the repository, including
      # those not reachable by any reference.
      rpc :ListAllCommits, ::Gitaly::ListAllCommitsRequest, stream(::Gitaly::ListAllCommitsResponse)
      rpc :CommitIsAncestor, ::Gitaly::CommitIsAncestorRequest, ::Gitaly::CommitIsAncestorResponse
      rpc :TreeEntry, ::Gitaly::TreeEntryRequest, stream(::Gitaly::TreeEntryResponse)
      rpc :CountCommits, ::Gitaly::CountCommitsRequest, ::Gitaly::CountCommitsResponse
      rpc :CountDivergingCommits, ::Gitaly::CountDivergingCommitsRequest, ::Gitaly::CountDivergingCommitsResponse
      rpc :GetTreeEntries, ::Gitaly::GetTreeEntriesRequest, stream(::Gitaly::GetTreeEntriesResponse)
      rpc :ListFiles, ::Gitaly::ListFilesRequest, stream(::Gitaly::ListFilesResponse)
      rpc :FindCommit, ::Gitaly::FindCommitRequest, ::Gitaly::FindCommitResponse
      rpc :CommitStats, ::Gitaly::CommitStatsRequest, ::Gitaly::CommitStatsResponse
      # Use a stream to paginate the result set
      rpc :FindAllCommits, ::Gitaly::FindAllCommitsRequest, stream(::Gitaly::FindAllCommitsResponse)
      rpc :FindCommits, ::Gitaly::FindCommitsRequest, stream(::Gitaly::FindCommitsResponse)
      rpc :CommitLanguages, ::Gitaly::CommitLanguagesRequest, ::Gitaly::CommitLanguagesResponse
      rpc :RawBlame, ::Gitaly::RawBlameRequest, stream(::Gitaly::RawBlameResponse)
      rpc :LastCommitForPath, ::Gitaly::LastCommitForPathRequest, ::Gitaly::LastCommitForPathResponse
      rpc :ListLastCommitsForTree, ::Gitaly::ListLastCommitsForTreeRequest, stream(::Gitaly::ListLastCommitsForTreeResponse)
      rpc :CommitsByMessage, ::Gitaly::CommitsByMessageRequest, stream(::Gitaly::CommitsByMessageResponse)
      rpc :ListCommitsByOid, ::Gitaly::ListCommitsByOidRequest, stream(::Gitaly::ListCommitsByOidResponse)
      rpc :ListCommitsByRefName, ::Gitaly::ListCommitsByRefNameRequest, stream(::Gitaly::ListCommitsByRefNameResponse)
      rpc :FilterShasWithSignatures, stream(::Gitaly::FilterShasWithSignaturesRequest), stream(::Gitaly::FilterShasWithSignaturesResponse)
      rpc :GetCommitSignatures, ::Gitaly::GetCommitSignaturesRequest, stream(::Gitaly::GetCommitSignaturesResponse)
      rpc :GetCommitMessages, ::Gitaly::GetCommitMessagesRequest, stream(::Gitaly::GetCommitMessagesResponse)
    end

    Stub = Service.rpc_stub_class
  end
end
