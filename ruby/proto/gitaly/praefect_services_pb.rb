# Generated by the protocol buffer compiler.  DO NOT EDIT!
# Source: praefect.proto for package 'gitaly'

require 'grpc'
require 'praefect_pb'

module Gitaly
  module PraefectInfoService
    class Service

      include ::GRPC::GenericService

      self.marshal_class_method = :encode
      self.unmarshal_class_method = :decode
      self.service_name = 'gitaly.PraefectInfoService'

      rpc :RepositoryReplicas, ::Gitaly::RepositoryReplicasRequest, ::Gitaly::RepositoryReplicasResponse
      # DatalossCheck checks for unavailable repositories.
      rpc :DatalossCheck, ::Gitaly::DatalossCheckRequest, ::Gitaly::DatalossCheckResponse
      # SetAuthoritativeStorage sets the authoritative storage for a repository on a given virtual storage.
      # This causes the current version of the repository on the authoritative storage to be considered the
      # latest and overwrite any other version on the virtual storage.
      rpc :SetAuthoritativeStorage, ::Gitaly::SetAuthoritativeStorageRequest, ::Gitaly::SetAuthoritativeStorageResponse
      # SetReplicationFactor assigns or unassigns host nodes from the repository to meet the desired replication factor.
      # SetReplicationFactor returns an error when trying to set a replication factor that exceeds the storage node count
      # in the virtual storage. An error is also returned when trying to set a replication factor below one. The primary node
      # won't be unassigned as it needs a copy of the repository to accept writes. Likewise, the primary is the first storage
      # that gets assigned when setting a replication factor for a repository. Assignments of unconfigured storages are ignored.
      # This might cause the actual replication factor to be higher than desired if the replication factor is set during an upgrade
      # from a Praefect node that does not yet know about a new node. As assignments of unconfigured storages are ignored, replication
      # factor of repositories assigned to a storage node removed from the cluster is effectively decreased.
      rpc :SetReplicationFactor, ::Gitaly::SetReplicationFactorRequest, ::Gitaly::SetReplicationFactorResponse
      # GetRepositoryMetadata returns the cluster metadata for a repository. Returns NotFound if the repository does not exist.
      rpc :GetRepositoryMetadata, ::Gitaly::GetRepositoryMetadataRequest, ::Gitaly::GetRepositoryMetadataResponse
    end

    Stub = Service.rpc_stub_class
  end
end
