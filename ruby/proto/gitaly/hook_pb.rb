# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: hook.proto

require 'google/protobuf'

require 'lint_pb'
require 'shared_pb'
Google::Protobuf::DescriptorPool.generated_pool.build do
  add_message "gitaly.PreReceiveHookRequest" do
    optional :repository, :message, 1, "gitaly.Repository"
    repeated :environment_variables, :string, 2
    optional :stdin, :bytes, 4
    repeated :git_push_options, :string, 5
  end
  add_message "gitaly.PreReceiveHookResponse" do
    optional :stdout, :bytes, 1
    optional :stderr, :bytes, 2
    optional :exit_status, :message, 3, "gitaly.ExitStatus"
  end
  add_message "gitaly.PostReceiveHookRequest" do
    optional :repository, :message, 1, "gitaly.Repository"
    repeated :environment_variables, :string, 2
    optional :stdin, :bytes, 3
    repeated :git_push_options, :string, 4
  end
  add_message "gitaly.PostReceiveHookResponse" do
    optional :stdout, :bytes, 1
    optional :stderr, :bytes, 2
    optional :exit_status, :message, 3, "gitaly.ExitStatus"
  end
  add_message "gitaly.UpdateHookRequest" do
    optional :repository, :message, 1, "gitaly.Repository"
    repeated :environment_variables, :string, 2
    optional :ref, :bytes, 3
    optional :old_value, :string, 4
    optional :new_value, :string, 5
  end
  add_message "gitaly.UpdateHookResponse" do
    optional :stdout, :bytes, 1
    optional :stderr, :bytes, 2
    optional :exit_status, :message, 3, "gitaly.ExitStatus"
  end
  add_message "gitaly.ReferenceTransactionHookRequest" do
    optional :repository, :message, 1, "gitaly.Repository"
    repeated :environment_variables, :string, 2
    optional :stdin, :bytes, 3
  end
  add_message "gitaly.ReferenceTransactionHookResponse" do
    optional :stdout, :bytes, 1
    optional :stderr, :bytes, 2
    optional :exit_status, :message, 3, "gitaly.ExitStatus"
  end
end

module Gitaly
  PreReceiveHookRequest = Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.PreReceiveHookRequest").msgclass
  PreReceiveHookResponse = Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.PreReceiveHookResponse").msgclass
  PostReceiveHookRequest = Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.PostReceiveHookRequest").msgclass
  PostReceiveHookResponse = Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.PostReceiveHookResponse").msgclass
  UpdateHookRequest = Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.UpdateHookRequest").msgclass
  UpdateHookResponse = Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.UpdateHookResponse").msgclass
  ReferenceTransactionHookRequest = Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ReferenceTransactionHookRequest").msgclass
  ReferenceTransactionHookResponse = Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.ReferenceTransactionHookResponse").msgclass
end
