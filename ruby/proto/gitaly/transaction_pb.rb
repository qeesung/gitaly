# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: transaction.proto

require 'lint_pb'
require 'shared_pb'
require 'google/protobuf'

Google::Protobuf::DescriptorPool.generated_pool.build do
  add_file("transaction.proto", :syntax => :proto3) do
    add_message "gitaly.VoteTransactionRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :transaction_id, :uint64, 2
      optional :node, :string, 3
      optional :reference_updates_hash, :bytes, 4
    end
    add_message "gitaly.VoteTransactionResponse" do
      optional :state, :enum, 1, "gitaly.VoteTransactionResponse.TransactionState"
    end
    add_enum "gitaly.VoteTransactionResponse.TransactionState" do
      value :COMMIT, 0
      value :ABORT, 1
      value :STOP, 2
    end
    add_message "gitaly.StopTransactionRequest" do
      optional :repository, :message, 1, "gitaly.Repository"
      optional :transaction_id, :uint64, 2
    end
    add_message "gitaly.StopTransactionResponse" do
    end
  end
end

module Gitaly
  VoteTransactionRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.VoteTransactionRequest").msgclass
  VoteTransactionResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.VoteTransactionResponse").msgclass
  VoteTransactionResponse::TransactionState = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.VoteTransactionResponse.TransactionState").enummodule
  StopTransactionRequest = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.StopTransactionRequest").msgclass
  StopTransactionResponse = ::Google::Protobuf::DescriptorPool.generated_pool.lookup("gitaly.StopTransactionResponse").msgclass
end
