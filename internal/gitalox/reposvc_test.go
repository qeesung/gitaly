package gitalox_test

import (
	"context"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"google.golang.org/grpc"
)

// mockRepoSvc is a mock implementation of gitalypb.RepositoryServiceServer
// for testing purposes
type mockDownstreamRepoSvc struct {
	srv *grpc.Server
}

func (m *mockDownstreamRepoSvc) RepositoryExists(context.Context, *gitalypb.RepositoryExistsRequest) (*gitalypb.RepositoryExistsResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) RepackIncremental(context.Context, *gitalypb.RepackIncrementalRequest) (*gitalypb.RepackIncrementalResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) RepackFull(context.Context, *gitalypb.RepackFullRequest) (*gitalypb.RepackFullResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) GarbageCollect(context.Context, *gitalypb.GarbageCollectRequest) (*gitalypb.GarbageCollectResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) RepositorySize(context.Context, *gitalypb.RepositorySizeRequest) (*gitalypb.RepositorySizeResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) ApplyGitattributes(context.Context, *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) FetchRemote(context.Context, *gitalypb.FetchRemoteRequest) (*gitalypb.FetchRemoteResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) CreateRepository(context.Context, *gitalypb.CreateRepositoryRequest) (*gitalypb.CreateRepositoryResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) GetArchive(*gitalypb.GetArchiveRequest, gitalypb.RepositoryService_GetArchiveServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) HasLocalBranches(context.Context, *gitalypb.HasLocalBranchesRequest) (*gitalypb.HasLocalBranchesResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) FetchSourceBranch(context.Context, *gitalypb.FetchSourceBranchRequest) (*gitalypb.FetchSourceBranchResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) Fsck(context.Context, *gitalypb.FsckRequest) (*gitalypb.FsckResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) WriteRef(context.Context, *gitalypb.WriteRefRequest) (*gitalypb.WriteRefResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) FindMergeBase(context.Context, *gitalypb.FindMergeBaseRequest) (*gitalypb.FindMergeBaseResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) CreateFork(context.Context, *gitalypb.CreateForkRequest) (*gitalypb.CreateForkResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) IsRebaseInProgress(context.Context, *gitalypb.IsRebaseInProgressRequest) (*gitalypb.IsRebaseInProgressResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) IsSquashInProgress(context.Context, *gitalypb.IsSquashInProgressRequest) (*gitalypb.IsSquashInProgressResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) CreateRepositoryFromURL(context.Context, *gitalypb.CreateRepositoryFromURLRequest) (*gitalypb.CreateRepositoryFromURLResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) CreateBundle(*gitalypb.CreateBundleRequest, gitalypb.RepositoryService_CreateBundleServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) CreateRepositoryFromBundle(gitalypb.RepositoryService_CreateRepositoryFromBundleServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) WriteConfig(context.Context, *gitalypb.WriteConfigRequest) (*gitalypb.WriteConfigResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) SetConfig(context.Context, *gitalypb.SetConfigRequest) (*gitalypb.SetConfigResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) DeleteConfig(context.Context, *gitalypb.DeleteConfigRequest) (*gitalypb.DeleteConfigResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) FindLicense(context.Context, *gitalypb.FindLicenseRequest) (*gitalypb.FindLicenseResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) GetInfoAttributes(*gitalypb.GetInfoAttributesRequest, gitalypb.RepositoryService_GetInfoAttributesServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) CalculateChecksum(context.Context, *gitalypb.CalculateChecksumRequest) (*gitalypb.CalculateChecksumResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) Cleanup(context.Context, *gitalypb.CleanupRequest) (*gitalypb.CleanupResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) GetSnapshot(*gitalypb.GetSnapshotRequest, gitalypb.RepositoryService_GetSnapshotServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) CreateRepositoryFromSnapshot(context.Context, *gitalypb.CreateRepositoryFromSnapshotRequest) (*gitalypb.CreateRepositoryFromSnapshotResponse, error) {
	return nil, nil
}

func (m *mockDownstreamRepoSvc) GetRawChanges(*gitalypb.GetRawChangesRequest, gitalypb.RepositoryService_GetRawChangesServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) SearchFilesByContent(*gitalypb.SearchFilesByContentRequest, gitalypb.RepositoryService_SearchFilesByContentServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) SearchFilesByName(*gitalypb.SearchFilesByNameRequest, gitalypb.RepositoryService_SearchFilesByNameServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) RestoreCustomHooks(gitalypb.RepositoryService_RestoreCustomHooksServer) error {
	return nil
}

func (m *mockDownstreamRepoSvc) BackupCustomHooks(*gitalypb.BackupCustomHooksRequest, gitalypb.RepositoryService_BackupCustomHooksServer) error {
	return nil
}
