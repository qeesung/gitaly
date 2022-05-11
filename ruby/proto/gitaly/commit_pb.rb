# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: commit.proto

require 'google/protobuf'

require 'google/protobuf/timestamp_pb'
require 'lint_pb'
require 'shared_pb'

Google::Protobuf::DescriptorPool.generated_pool.build do
  add_file("commit.proto", :syntax => :proto3) do
    add_message "gitaly.ListCommitsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :revisions, :string, 2
      optional :pagination_params, :message, 3, "gitaly.PaginationParameter"
      optional :order, :enum, 4, "gitaly.ListCommitsRequest.Order"
      optional :reverse, :bool, 11
      optional :max_parents, :uint32, 5
      optional :disable_walk, :bool, 6
      optional :first_parent, :bool, 7
      optional :after, :message, 8, "google.protobuf.Timestamp"
      optional :before, :message, 9, "google.protobuf.Timestamp"
      optional :author, :bytes, 10
    end
    add_enum "gitaly.ListCommitsRequest.Order" do
      value :NONE, 0
      value :TOPO, 1
      value :DATE, 2
    end
    add_message "gitaly.ListCommitsResponse" do
      repeated :commits, :message, 1, "gitaly.GitCommit"
    end
    add_message "gitaly.ListAllCommitsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :pagination_params, :message, 2, "gitaly.PaginationParameter"
    end
    add_message "gitaly.ListAllCommitsResponse" do
      repeated :commits, :message, 1, "gitaly.GitCommit"
    end
    add_message "gitaly.CommitStatsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
    end
    add_message "gitaly.CommitStatsResponse" do
      optional :oid, :string, 1
      optional :additions, :int32, 2
      optional :deletions, :int32, 3
    end
    add_message "gitaly.CommitIsAncestorRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :ancestor_id, :string, 2
      optional :child_id, :string, 3
    end
    add_message "gitaly.CommitIsAncestorResponse" do
      optional :value, :bool, 1
    end
    add_message "gitaly.TreeEntryRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :path, :bytes, 3
      optional :limit, :int64, 4
      optional :max_size, :int64, 5
    end
    add_message "gitaly.TreeEntryResponse" do
      optional :type, :enum, 1, "gitaly.TreeEntryResponse.ObjectType"
      optional :oid, :string, 2
      optional :size, :int64, 3
      optional :mode, :int32, 4
      optional :data, :bytes, 5
    end
    add_enum "gitaly.TreeEntryResponse.ObjectType" do
      value :COMMIT, 0
      value :BLOB, 1
      value :TREE, 2
      value :TAG, 3
    end
    add_message "gitaly.CountCommitsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :after, :message, 3, "google.protobuf.Timestamp"
      optional :before, :message, 4, "google.protobuf.Timestamp"
      optional :path, :bytes, 5
      optional :max_count, :int32, 6
      optional :all, :bool, 7
      optional :first_parent, :bool, 8
      optional :global_options, :message, 9, "gitaly.GlobalOptions"
    end
    add_message "gitaly.CountCommitsResponse" do
      optional :count, :int32, 1
    end
    add_message "gitaly.CountDivergingCommitsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :from, :bytes, 2
      optional :to, :bytes, 3
      optional :max_count, :int32, 7
    end
    add_message "gitaly.CountDivergingCommitsResponse" do
      optional :left_count, :int32, 1
      optional :right_count, :int32, 2
    end
    add_message "gitaly.TreeEntry" do
      optional :oid, :string, 1
      optional :root_oid, :string, 2
      optional :path, :bytes, 3
      optional :type, :enum, 4, "gitaly.TreeEntry.EntryType"
      optional :mode, :int32, 5
      optional :commit_oid, :string, 6
      optional :flat_path, :bytes, 7
    end
    add_enum "gitaly.TreeEntry.EntryType" do
      value :BLOB, 0
      value :TREE, 1
      value :COMMIT, 3
    end
    add_message "gitaly.GetTreeEntriesRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :path, :bytes, 3
      optional :recursive, :bool, 4
      optional :sort, :enum, 5, "gitaly.GetTreeEntriesRequest.SortBy"
      optional :pagination_params, :message, 6, "gitaly.PaginationParameter"
    end
    add_enum "gitaly.GetTreeEntriesRequest.SortBy" do
      value :DEFAULT, 0
      value :TREES_FIRST, 1
    end
    add_message "gitaly.GetTreeEntriesResponse" do
      repeated :entries, :message, 1, "gitaly.TreeEntry"
      optional :pagination_cursor, :message, 2, "gitaly.PaginationCursor"
    end
    add_message "gitaly.ListFilesRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
    end
    add_message "gitaly.ListFilesResponse" do
      repeated :paths, :bytes, 1
    end
    add_message "gitaly.FindCommitRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :trailers, :bool, 3
    end
    add_message "gitaly.FindCommitResponse" do
      optional :commit, :message, 1, "gitaly.GitCommit"
    end
    add_message "gitaly.ListCommitsByOidRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :oid, :string, 2
    end
    add_message "gitaly.ListCommitsByOidResponse" do
      repeated :commits, :message, 1, "gitaly.GitCommit"
    end
    add_message "gitaly.ListCommitsByRefNameRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :ref_names, :bytes, 2
    end
    add_message "gitaly.ListCommitsByRefNameResponse" do
      repeated :commit_refs, :message, 2, "gitaly.ListCommitsByRefNameResponse.CommitForRef"
    end
    add_message "gitaly.ListCommitsByRefNameResponse.CommitForRef" do
      optional :commit, :message, 1, "gitaly.GitCommit"
      optional :ref_name, :bytes, 2
    end
    add_message "gitaly.FindAllCommitsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :max_count, :int32, 3
      optional :skip, :int32, 4
      optional :order, :enum, 5, "gitaly.FindAllCommitsRequest.Order"
    end
    add_enum "gitaly.FindAllCommitsRequest.Order" do
      value :NONE, 0
      value :TOPO, 1
      value :DATE, 2
    end
    add_message "gitaly.FindAllCommitsResponse" do
      repeated :commits, :message, 1, "gitaly.GitCommit"
    end
    add_message "gitaly.FindCommitsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :limit, :int32, 3
      optional :offset, :int32, 4
      repeated :paths, :bytes, 5
      optional :follow, :bool, 6
      optional :skip_merges, :bool, 7
      optional :disable_walk, :bool, 8
      optional :after, :message, 9, "google.protobuf.Timestamp"
      optional :before, :message, 10, "google.protobuf.Timestamp"
      optional :all, :bool, 11
      optional :first_parent, :bool, 12
      optional :author, :bytes, 13
      optional :order, :enum, 14, "gitaly.FindCommitsRequest.Order"
      optional :global_options, :message, 15, "gitaly.GlobalOptions"
      optional :trailers, :bool, 16
    end
    add_enum "gitaly.FindCommitsRequest.Order" do
      value :NONE, 0
      value :TOPO, 1
    end
    add_message "gitaly.FindCommitsResponse" do
      repeated :commits, :message, 1, "gitaly.GitCommit"
    end
    add_message "gitaly.CommitLanguagesRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
    end
    add_message "gitaly.CommitLanguagesResponse" do
      repeated :languages, :message, 1, "gitaly.CommitLanguagesResponse.Language"
    end
    add_message "gitaly.CommitLanguagesResponse.Language" do
      optional :name, :string, 1
      optional :share, :float, 2
      optional :color, :string, 3
      optional :file_count, :uint32, 4
      optional :bytes, :uint64, 5
    end
    add_message "gitaly.RawBlameRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :path, :bytes, 3
      optional :range, :bytes, 4
    end
    add_message "gitaly.RawBlameResponse" do
      optional :data, :bytes, 1
    end
    add_message "gitaly.LastCommitForPathRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :path, :bytes, 3
      optional :literal_pathspec, :bool, 4
      optional :global_options, :message, 5, "gitaly.GlobalOptions"
    end
    add_message "gitaly.LastCommitForPathResponse" do
      optional :commit, :message, 1, "gitaly.GitCommit"
    end
    add_message "gitaly.ListLastCommitsForTreeRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :string, 2
      optional :path, :bytes, 3
      optional :limit, :int32, 4
      optional :offset, :int32, 5
      optional :literal_pathspec, :bool, 6
      optional :global_options, :message, 7, "gitaly.GlobalOptions"
    end
    add_message "gitaly.ListLastCommitsForTreeResponse" do
      repeated :commits, :message, 1, "gitaly.ListLastCommitsForTreeResponse.CommitForTree"
    end
    add_message "gitaly.ListLastCommitsForTreeResponse.CommitForTree" do
      optional :commit, :message, 2, "gitaly.GitCommit"
      optional :path_bytes, :bytes, 4
    end
    add_message "gitaly.CommitsByMessageRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :revision, :bytes, 2
      optional :offset, :int32, 3
      optional :limit, :int32, 4
      optional :path, :bytes, 5
      optional :query, :string, 6
      optional :global_options, :message, 7, "gitaly.GlobalOptions"
    end
    add_message "gitaly.CommitsByMessageResponse" do
      repeated :commits, :message, 1, "gitaly.GitCommit"
    end
    add_message "gitaly.FilterShasWithSignaturesRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :shas, :bytes, 2
    end
    add_message "gitaly.FilterShasWithSignaturesResponse" do
      repeated :shas, :bytes, 1
    end
    add_message "gitaly.ExtractCommitSignatureRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :commit_id, :string, 2
    end
    add_message "gitaly.ExtractCommitSignatureResponse" do
      optional :signature, :bytes, 1
      optional :signed_text, :bytes, 2
    end
    add_message "gitaly.GetCommitSignaturesRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :commit_ids, :string, 2
    end
    add_message "gitaly.GetCommitSignaturesResponse" do
      optional :commit_id, :string, 1
      optional :signature, :bytes, 2
      optional :signed_text, :bytes, 3
    end
    add_message "gitaly.GetCommitMessagesRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :commit_ids, :string, 2
    end
    add_message "gitaly.GetCommitMessagesResponse" do
      optional :commit_id, :string, 1
      optional :message, :bytes, 2
    end
    add_message "gitaly.CheckObjectsExistRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :revisions, :bytes, 2
    end
    add_message "gitaly.CheckObjectsExistResponse" do
      repeated :revisions, :message, 1, "gitaly.CheckObjectsExistResponse.RevisionExistence"
    end
    add_message "gitaly.CheckObjectsExistResponse.RevisionExistence" do
      optional :name, :bytes, 1
      optional :exists, :bool, 2
    end
  end
end

module Gitaly
  ListCommitsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListCommitsRequest").msgclass
  ListCommitsRequest::Order = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListCommitsRequest.Order").enummodule
  ListCommitsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListCommitsResponse").msgclass
  ListAllCommitsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListAllCommitsRequest").msgclass
  ListAllCommitsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListAllCommitsResponse").msgclass
  CommitStatsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitStatsRequest").msgclass
  CommitStatsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitStatsResponse").msgclass
  CommitIsAncestorRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitIsAncestorRequest").msgclass
  CommitIsAncestorResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitIsAncestorResponse").msgclass
  TreeEntryRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.TreeEntryRequest").msgclass
  TreeEntryResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.TreeEntryResponse").msgclass
  TreeEntryResponse::ObjectType = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.TreeEntryResponse.ObjectType").enummodule
  CountCommitsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CountCommitsRequest").msgclass
  CountCommitsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CountCommitsResponse").msgclass
  CountDivergingCommitsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CountDivergingCommitsRequest").msgclass
  CountDivergingCommitsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CountDivergingCommitsResponse").msgclass
  TreeEntry = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.TreeEntry").msgclass
  TreeEntry::EntryType = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.TreeEntry.EntryType").enummodule
  GetTreeEntriesRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetTreeEntriesRequest").msgclass
  GetTreeEntriesRequest::SortBy = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetTreeEntriesRequest.SortBy").enummodule
  GetTreeEntriesResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetTreeEntriesResponse").msgclass
  ListFilesRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListFilesRequest").msgclass
  ListFilesResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListFilesResponse").msgclass
  FindCommitRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindCommitRequest").msgclass
  FindCommitResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindCommitResponse").msgclass
  ListCommitsByOidRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListCommitsByOidRequest").msgclass
  ListCommitsByOidResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListCommitsByOidResponse").msgclass
  ListCommitsByRefNameRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListCommitsByRefNameRequest").msgclass
  ListCommitsByRefNameResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListCommitsByRefNameResponse").msgclass
  ListCommitsByRefNameResponse::CommitForRef = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListCommitsByRefNameResponse.CommitForRef").msgclass
  FindAllCommitsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindAllCommitsRequest").msgclass
  FindAllCommitsRequest::Order = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindAllCommitsRequest.Order").enummodule
  FindAllCommitsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindAllCommitsResponse").msgclass
  FindCommitsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindCommitsRequest").msgclass
  FindCommitsRequest::Order = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindCommitsRequest.Order").enummodule
  FindCommitsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindCommitsResponse").msgclass
  CommitLanguagesRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitLanguagesRequest").msgclass
  CommitLanguagesResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitLanguagesResponse").msgclass
  CommitLanguagesResponse::Language = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitLanguagesResponse.Language").msgclass
  RawBlameRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.RawBlameRequest").msgclass
  RawBlameResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.RawBlameResponse").msgclass
  LastCommitForPathRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.LastCommitForPathRequest").msgclass
  LastCommitForPathResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.LastCommitForPathResponse").msgclass
  ListLastCommitsForTreeRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListLastCommitsForTreeRequest").msgclass
  ListLastCommitsForTreeResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListLastCommitsForTreeResponse").msgclass
  ListLastCommitsForTreeResponse::CommitForTree = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListLastCommitsForTreeResponse.CommitForTree").msgclass
  CommitsByMessageRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitsByMessageRequest").msgclass
  CommitsByMessageResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitsByMessageResponse").msgclass
  FilterShasWithSignaturesRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FilterShasWithSignaturesRequest").msgclass
  FilterShasWithSignaturesResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FilterShasWithSignaturesResponse").msgclass
  ExtractCommitSignatureRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ExtractCommitSignatureRequest").msgclass
  ExtractCommitSignatureResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ExtractCommitSignatureResponse").msgclass
  GetCommitSignaturesRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetCommitSignaturesRequest").msgclass
  GetCommitSignaturesResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetCommitSignaturesResponse").msgclass
  GetCommitMessagesRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetCommitMessagesRequest").msgclass
  GetCommitMessagesResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetCommitMessagesResponse").msgclass
  CheckObjectsExistRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CheckObjectsExistRequest").msgclass
  CheckObjectsExistResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CheckObjectsExistResponse").msgclass
  CheckObjectsExistResponse::RevisionExistence = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CheckObjectsExistResponse.RevisionExistence").msgclass
end
