# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: errors.proto

require 'google/protobuf/duration_pb'
require 'google/protobuf'

Google::Protobuf::DescriptorPool.generated_pool.build do
  add_file("errors.proto", :syntax => :proto3) do
    add_message "gitaly.AccessCheckError" do
      optional :error_message, :string, 1
      optional :protocol, :string, 2
      optional :user_id, :string, 3
      optional :changes, :bytes, 4
    end
    add_message "gitaly.InvalidRefFormatError" do
      repeated :refs, :bytes, 2
    end
    add_message "gitaly.NotAncestorError" do
      optional :parent_revision, :bytes, 1
      optional :child_revision, :bytes, 2
    end
    add_message "gitaly.ChangesAlreadyAppliedError" do
    end
    add_message "gitaly.MergeConflictError" do
      repeated :conflicting_files, :bytes, 1
      repeated :conflicting_commit_ids, :string, 2
    end
    add_message "gitaly.ReferencesLockedError" do
    end
    add_message "gitaly.ReferenceUpdateError" do
      optional :reference_name, :bytes, 1
      optional :old_oid, :string, 2
      optional :new_oid, :string, 3
    end
    add_message "gitaly.ResolveRevisionError" do
      optional :revision, :bytes, 1
    end
    add_message "gitaly.LimitError" do
      optional :error_message, :string, 1
      optional :retry_after, :message, 2, "google.protobuf.Duration"
    end
    add_message "gitaly.CustomHookError" do
      optional :stdout, :bytes, 1
      optional :stderr, :bytes, 2
      optional :hook_type, :enum, 3, "gitaly.CustomHookError.HookType"
    end
    add_enum "gitaly.CustomHookError.HookType" do
      value :HOOK_TYPE_UNSPECIFIED, 0
      value :HOOK_TYPE_PRERECEIVE, 1
      value :HOOK_TYPE_UPDATE, 2
      value :HOOK_TYPE_POSTRECEIVE, 3
    end
  end
end

module Gitaly
  AccessCheckError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.AccessCheckError").msgclass
  InvalidRefFormatError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.InvalidRefFormatError").msgclass
  NotAncestorError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.NotAncestorError").msgclass
  ChangesAlreadyAppliedError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ChangesAlreadyAppliedError").msgclass
  MergeConflictError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.MergeConflictError").msgclass
  ReferencesLockedError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ReferencesLockedError").msgclass
  ReferenceUpdateError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ReferenceUpdateError").msgclass
  ResolveRevisionError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ResolveRevisionError").msgclass
  LimitError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.LimitError").msgclass
  CustomHookError = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CustomHookError").msgclass
  CustomHookError::HookType = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.CustomHookError.HookType").enummodule
end
