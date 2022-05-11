# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: blob.proto

require 'google/protobuf'

require 'lint_pb'
require 'shared_pb'

Google::Protobuf::DescriptorPool.generated_pool.build do
  add_file("blob.proto", :syntax => :proto3) do
    add_message "gitaly.GetBlobRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :oid, :string, 2
      optional :limit, :int64, 3
    end
    add_message "gitaly.GetBlobResponse" do
      optional :size, :int64, 1
      optional :data, :bytes, 2
      optional :oid, :string, 3
    end
    add_message "gitaly.GetBlobsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :revision_paths, :message, 2, "gitaly.GetBlobsRequest.RevisionPath"
      optional :limit, :int64, 3
    end
    add_message "gitaly.GetBlobsRequest.RevisionPath" do
      optional :revision, :string, 1
      optional :path, :bytes, 2
    end
    add_message "gitaly.GetBlobsResponse" do
      optional :size, :int64, 1
      optional :data, :bytes, 2
      optional :oid, :string, 3
      optional :is_submodule, :bool, 4
      optional :mode, :int32, 5
      optional :revision, :string, 6
      optional :path, :bytes, 7
      optional :type, :enum, 8, "gitaly.ObjectType"
    end
    add_message "gitaly.ListBlobsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :revisions, :string, 2
      optional :limit, :uint32, 3
      optional :bytes_limit, :int64, 4
      optional :with_paths, :bool, 5
    end
    add_message "gitaly.ListBlobsResponse" do
      repeated :blobs, :message, 1, "gitaly.ListBlobsResponse.Blob"
    end
    add_message "gitaly.ListBlobsResponse.Blob" do
      optional :oid, :string, 1
      optional :size, :int64, 2
      optional :data, :bytes, 3
      optional :path, :bytes, 4
    end
    add_message "gitaly.ListAllBlobsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :limit, :uint32, 2
      optional :bytes_limit, :int64, 3
    end
    add_message "gitaly.ListAllBlobsResponse" do
      repeated :blobs, :message, 1, "gitaly.ListAllBlobsResponse.Blob"
    end
    add_message "gitaly.ListAllBlobsResponse.Blob" do
      optional :oid, :string, 1
      optional :size, :int64, 2
      optional :data, :bytes, 3
    end
    add_message "gitaly.LFSPointer" do
      optional :size, :int64, 1
      optional :data, :bytes, 2
      optional :oid, :string, 3
    end
    add_message "gitaly.NewBlobObject" do
      optional :size, :int64, 1
      optional :oid, :string, 2
      optional :path, :bytes, 3
    end
    add_message "gitaly.GetLFSPointersRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :blob_ids, :string, 2
    end
    add_message "gitaly.GetLFSPointersResponse" do
      repeated :lfs_pointers, :message, 1, "gitaly.LFSPointer"
    end
    add_message "gitaly.ListLFSPointersRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :revisions, :string, 2
      optional :limit, :int32, 3
    end
    add_message "gitaly.ListLFSPointersResponse" do
      repeated :lfs_pointers, :message, 1, "gitaly.LFSPointer"
    end
    add_message "gitaly.ListAllLFSPointersRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :limit, :int32, 3
    end
    add_message "gitaly.ListAllLFSPointersResponse" do
      repeated :lfs_pointers, :message, 1, "gitaly.LFSPointer"
    end
  end
end

module Gitaly
  GetBlobRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetBlobRequest").msgclass
  GetBlobResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetBlobResponse").msgclass
  GetBlobsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetBlobsRequest").msgclass
  GetBlobsRequest::RevisionPath = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetBlobsRequest.RevisionPath").msgclass
  GetBlobsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetBlobsResponse").msgclass
  ListBlobsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListBlobsRequest").msgclass
  ListBlobsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListBlobsResponse").msgclass
  ListBlobsResponse::Blob = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListBlobsResponse.Blob").msgclass
  ListAllBlobsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListAllBlobsRequest").msgclass
  ListAllBlobsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListAllBlobsResponse").msgclass
  ListAllBlobsResponse::Blob = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListAllBlobsResponse.Blob").msgclass
  LFSPointer = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.LFSPointer").msgclass
  NewBlobObject = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.NewBlobObject").msgclass
  GetLFSPointersRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetLFSPointersRequest").msgclass
  GetLFSPointersResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.GetLFSPointersResponse").msgclass
  ListLFSPointersRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListLFSPointersRequest").msgclass
  ListLFSPointersResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListLFSPointersResponse").msgclass
  ListAllLFSPointersRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListAllLFSPointersRequest").msgclass
  ListAllLFSPointersResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ListAllLFSPointersResponse").msgclass
end
