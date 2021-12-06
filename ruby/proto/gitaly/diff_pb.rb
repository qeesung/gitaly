# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: diff.proto

require 'lint_pb'
require 'shared_pb'
require 'google/protobuf'

Google::Protobuf::DescriptorPool.generated_pool.build do
  add_file("diff.proto", :syntax => :proto3) do
    add_message "gitaly.CommitDiffRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :left_commit_id, :string, 2
      optional :right_commit_id, :string, 3
      optional :ignore_whitespace_change, :bool, 4
      repeated :paths, :bytes, 5
      optional :collapse_diffs, :bool, 6
      optional :enforce_limits, :bool, 7
      optional :max_files, :int32, 8
      optional :max_lines, :int32, 9
      optional :max_bytes, :int32, 10
      optional :max_patch_bytes, :int32, 14
      optional :safe_max_files, :int32, 11
      optional :safe_max_lines, :int32, 12
      optional :safe_max_bytes, :int32, 13
      optional :diff_mode, :enum, 15, "gitaly.CommitDiffRequest.DiffMode"
    end
    add_enum "gitaly.CommitDiffRequest.DiffMode" do
      value :DEFAULT, 0
      value :WORDDIFF, 1
    end
    add_message "gitaly.CommitDiffResponse" do
      optional :from_path, :bytes, 1
      optional :to_path, :bytes, 2
      optional :from_id, :string, 3
      optional :to_id, :string, 4
      optional :old_mode, :int32, 5
      optional :new_mode, :int32, 6
      optional :binary, :bool, 7
      optional :raw_patch_data, :bytes, 9
      optional :end_of_patch, :bool, 10
      optional :overflow_marker, :bool, 11
      optional :collapsed, :bool, 12
      optional :too_large, :bool, 13
    end
    add_message "gitaly.CommitDeltaRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :left_commit_id, :string, 2
      optional :right_commit_id, :string, 3
      repeated :paths, :bytes, 4
    end
    add_message "gitaly.CommitDelta" do
      optional :from_path, :bytes, 1
      optional :to_path, :bytes, 2
      optional :from_id, :string, 3
      optional :to_id, :string, 4
      optional :old_mode, :int32, 5
      optional :new_mode, :int32, 6
    end
    add_message "gitaly.CommitDeltaResponse" do
      repeated :deltas, :message, 1, "gitaly.CommitDelta"
    end
    add_message "gitaly.RawDiffRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :left_commit_id, :string, 2
      optional :right_commit_id, :string, 3
    end
    add_message "gitaly.RawDiffResponse" do
      optional :data, :bytes, 1
    end
    add_message "gitaly.RawPatchRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :left_commit_id, :string, 2
      optional :right_commit_id, :string, 3
    end
    add_message "gitaly.RawPatchResponse" do
      optional :data, :bytes, 1
    end
    add_message "gitaly.DiffStatsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :left_commit_id, :string, 2
      optional :right_commit_id, :string, 3
    end
    add_message "gitaly.DiffStats" do
      optional :path, :bytes, 1
      optional :additions, :int32, 2
      optional :deletions, :int32, 3
      optional :old_path, :bytes, 4
    end
    add_message "gitaly.DiffStatsResponse" do
      repeated :stats, :message, 1, "gitaly.DiffStats"
    end
    add_message "gitaly.FindChangedPathsRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      repeated :commits, :string, 2
    end
    add_message "gitaly.FindChangedPathsResponse" do
      repeated :paths, :message, 1, "gitaly.ChangedPaths"
    end
    add_message "gitaly.ChangedPaths" do
      optional :path, :bytes, 1
      optional :status, :enum, 2, "gitaly.ChangedPaths.Status"
    end
    add_enum "gitaly.ChangedPaths.Status" do
      value :ADDED, 0
      value :MODIFIED, 1
      value :DELETED, 2
      value :TYPE_CHANGE, 3
      value :COPIED, 4
    end
  end
end

module Gitaly
  CommitDiffRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitDiffRequest").msgclass
  CommitDiffRequest::DiffMode = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitDiffRequest.DiffMode").enummodule
  CommitDiffResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitDiffResponse").msgclass
  CommitDeltaRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitDeltaRequest").msgclass
  CommitDelta = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitDelta").msgclass
  CommitDeltaResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CommitDeltaResponse").msgclass
  RawDiffRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.RawDiffRequest").msgclass
  RawDiffResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.RawDiffResponse").msgclass
  RawPatchRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.RawPatchRequest").msgclass
  RawPatchResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.RawPatchResponse").msgclass
  DiffStatsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.DiffStatsRequest").msgclass
  DiffStats = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.DiffStats").msgclass
  DiffStatsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.DiffStatsResponse").msgclass
  FindChangedPathsRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindChangedPathsRequest").msgclass
  FindChangedPathsResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.FindChangedPathsResponse").msgclass
  ChangedPaths = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ChangedPaths").msgclass
  ChangedPaths::Status = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ChangedPaths.Status").enummodule
end
