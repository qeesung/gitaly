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
      # Return a stream so we can divide the response in chunks of branches
      rpc :FindLocalBranches, Gitaly::FindLocalBranchesRequest, stream(Gitaly::FindLocalBranchesResponse)
      rpc :FindAllBranches, Gitaly::FindAllBranchesRequest, stream(Gitaly::FindAllBranchesResponse)
      # Returns a stream of tags repository has.
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
      # GetTagSignatures returns signatures for annotated tags resolved from a set of revisions. Revisions
      # which don't resolve to an annotated tag are silently discarded. Revisions which cannot be resolved
      # result in an error. Tags which are annotated but not signed will return a TagSignature response
      # which has no signature, but its unsigned contents will still be returned.
      rpc :GetTagSignatures, Gitaly::GetTagSignaturesRequest, stream(Gitaly::GetTagSignaturesResponse)
      rpc :GetTagMessages, Gitaly::GetTagMessagesRequest, stream(Gitaly::GetTagMessagesResponse)
      # Returns commits that are only reachable from the ref passed
      rpc :ListNewCommits, Gitaly::ListNewCommitsRequest, stream(Gitaly::ListNewCommitsResponse)
      rpc :PackRefs, Gitaly::PackRefsRequest, Gitaly::PackRefsResponse
      # ListRefs returns a stream of all references in the repository. By default, pseudo-revisions like HEAD
      # will not be returned by this RPC. Any symbolic references will be resolved to the object ID it is
      # pointing at.
      rpc :ListRefs, Gitaly::ListRefsRequest, stream(Gitaly::ListRefsResponse)
      # ForEachRefWithObjects returns a for-each-ref query joined with the
      # objects the refs point to. The response is a byte stream in the format
      # of 'git cat-file --batch'. The refname is the last field on each info
      # line. If a tag object occurs in the output stream, the object
      # immediately after it is the recursively dereferenced object the tag
      # points to.
      rpc :ForEachRefWithObjects, Gitaly::ForEachRefWithObjectsRequest, stream(Gitaly::ForEachRefWithObjectsResponse)
    end

    Stub = Service.rpc_stub_class
  end
end
