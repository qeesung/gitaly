# Generated by the protocol buffer compiler.  DO NOT EDIT!
# Source: ref.proto for package 'gitaly'

require 'grpc'
require 'ref_pb'

module Gitaly
  module RefService
    class Service

      include GRPC::GenericService

      self.marshal_class_method = :encode
      self.unmarshal_class_method = :decode
      self.service_name = 'gitaly.RefService'

      rpc :FindDefaultBranchName, Gitaly::FindDefaultBranchNameRequest, Gitaly::FindDefaultBranchNameResponse
      rpc :FindAllBranchNames, Gitaly::FindAllBranchNamesRequest, stream(Gitaly::FindAllBranchNamesResponse)
      rpc :FindAllTagNames, Gitaly::FindAllTagNamesRequest, stream(Gitaly::FindAllTagNamesResponse)
      # Find a Ref matching the given constraints. Response may be empty.
      rpc :FindRefName, Gitaly::FindRefNameRequest, Gitaly::FindRefNameResponse
      # Return a stream so we can divide the response in chunks of branches
      rpc :FindLocalBranches, Gitaly::FindLocalBranchesRequest, stream(Gitaly::FindLocalBranchesResponse)
      rpc :FindAllBranches, Gitaly::FindAllBranchesRequest, stream(Gitaly::FindAllBranchesResponse)
      rpc :FindAllTags, Gitaly::FindAllTagsRequest, stream(Gitaly::FindAllTagsResponse)
      rpc :FindTag, Gitaly::FindTagRequest, Gitaly::FindTagResponse
      rpc :FindAllRemoteBranches, Gitaly::FindAllRemoteBranchesRequest, stream(Gitaly::FindAllRemoteBranchesResponse)
      rpc :RefExists, Gitaly::RefExistsRequest, Gitaly::RefExistsResponse
      # FindBranch finds a branch by its unqualified name (like "master") and
      # returns the commit it currently points to.
      rpc :FindBranch, Gitaly::FindBranchRequest, Gitaly::FindBranchResponse
      rpc :DeleteRefs, Gitaly::DeleteRefsRequest, Gitaly::DeleteRefsResponse
      rpc :ListBranchNamesContainingCommit, Gitaly::ListBranchNamesContainingCommitRequest, stream(Gitaly::ListBranchNamesContainingCommitResponse)
      rpc :ListTagNamesContainingCommit, Gitaly::ListTagNamesContainingCommitRequest, stream(Gitaly::ListTagNamesContainingCommitResponse)
      rpc :GetTagSignatures, Gitaly::GetTagSignaturesRequest, stream(Gitaly::GetTagSignaturesResponse)
      rpc :GetTagMessages, Gitaly::GetTagMessagesRequest, stream(Gitaly::GetTagMessagesResponse)
      # Returns commits that are only reachable from the ref passed
      rpc :ListNewCommits, Gitaly::ListNewCommitsRequest, stream(Gitaly::ListNewCommitsResponse)
      rpc :ListNewBlobs, Gitaly::ListNewBlobsRequest, stream(Gitaly::ListNewBlobsResponse)
      rpc :PackRefs, Gitaly::PackRefsRequest, Gitaly::PackRefsResponse
    end

    Stub = Service.rpc_stub_class
  end
end
