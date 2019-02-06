package praefect_test

import (
	"context"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"google.golang.org/grpc"
)

// mockRepoSvc is a mock implementation of gitalypb.RepositoryServiceServer
// for testing purposes
type mockRepoSvc struct {
	srv *grpc.Server
}

func (m *mockRepoSvc) RepositoryExists(context.Context, *gitalypb.RepositoryExistsRequest) (*gitalypb.RepositoryExistsResponse, error) {
	return &gitalypb.RepositoryExistsResponse{}, nil
}

func (m *mockRepoSvc) RepackIncremental(context.Context, *gitalypb.RepackIncrementalRequest) (*gitalypb.RepackIncrementalResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) RepackFull(context.Context, *gitalypb.RepackFullRequest) (*gitalypb.RepackFullResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) GarbageCollect(context.Context, *gitalypb.GarbageCollectRequest) (*gitalypb.GarbageCollectResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) RepositorySize(context.Context, *gitalypb.RepositorySizeRequest) (*gitalypb.RepositorySizeResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) ApplyGitattributes(context.Context, *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) FetchRemote(context.Context, *gitalypb.FetchRemoteRequest) (*gitalypb.FetchRemoteResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) CreateRepository(context.Context, *gitalypb.CreateRepositoryRequest) (*gitalypb.CreateRepositoryResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) GetArchive(*gitalypb.GetArchiveRequest, gitalypb.RepositoryService_GetArchiveServer) error {
	return nil
}

func (m *mockRepoSvc) HasLocalBranches(context.Context, *gitalypb.HasLocalBranchesRequest) (*gitalypb.HasLocalBranchesResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) FetchSourceBranch(context.Context, *gitalypb.FetchSourceBranchRequest) (*gitalypb.FetchSourceBranchResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) Fsck(context.Context, *gitalypb.FsckRequest) (*gitalypb.FsckResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) WriteRef(context.Context, *gitalypb.WriteRefRequest) (*gitalypb.WriteRefResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) FindMergeBase(context.Context, *gitalypb.FindMergeBaseRequest) (*gitalypb.FindMergeBaseResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) CreateFork(context.Context, *gitalypb.CreateForkRequest) (*gitalypb.CreateForkResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) IsRebaseInProgress(context.Context, *gitalypb.IsRebaseInProgressRequest) (*gitalypb.IsRebaseInProgressResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) IsSquashInProgress(context.Context, *gitalypb.IsSquashInProgressRequest) (*gitalypb.IsSquashInProgressResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) CreateRepositoryFromURL(context.Context, *gitalypb.CreateRepositoryFromURLRequest) (*gitalypb.CreateRepositoryFromURLResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) CreateBundle(*gitalypb.CreateBundleRequest, gitalypb.RepositoryService_CreateBundleServer) error {
	return nil
}

func (m *mockRepoSvc) CreateRepositoryFromBundle(gitalypb.RepositoryService_CreateRepositoryFromBundleServer) error {
	return nil
}

func (m *mockRepoSvc) WriteConfig(context.Context, *gitalypb.WriteConfigRequest) (*gitalypb.WriteConfigResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) SetConfig(context.Context, *gitalypb.SetConfigRequest) (*gitalypb.SetConfigResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) DeleteConfig(context.Context, *gitalypb.DeleteConfigRequest) (*gitalypb.DeleteConfigResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) FindLicense(context.Context, *gitalypb.FindLicenseRequest) (*gitalypb.FindLicenseResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) GetInfoAttributes(*gitalypb.GetInfoAttributesRequest, gitalypb.RepositoryService_GetInfoAttributesServer) error {
	return nil
}

func (m *mockRepoSvc) CalculateChecksum(context.Context, *gitalypb.CalculateChecksumRequest) (*gitalypb.CalculateChecksumResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) Cleanup(context.Context, *gitalypb.CleanupRequest) (*gitalypb.CleanupResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) GetSnapshot(*gitalypb.GetSnapshotRequest, gitalypb.RepositoryService_GetSnapshotServer) error {
	return nil
}

func (m *mockRepoSvc) CreateRepositoryFromSnapshot(context.Context, *gitalypb.CreateRepositoryFromSnapshotRequest) (*gitalypb.CreateRepositoryFromSnapshotResponse, error) {
	return nil, nil
}

func (m *mockRepoSvc) GetRawChanges(*gitalypb.GetRawChangesRequest, gitalypb.RepositoryService_GetRawChangesServer) error {
	return nil
}

func (m *mockRepoSvc) SearchFilesByContent(*gitalypb.SearchFilesByContentRequest, gitalypb.RepositoryService_SearchFilesByContentServer) error {
	return nil
}

func (m *mockRepoSvc) SearchFilesByName(*gitalypb.SearchFilesByNameRequest, gitalypb.RepositoryService_SearchFilesByNameServer) error {
	return nil
}

func (m *mockRepoSvc) RestoreCustomHooks(gitalypb.RepositoryService_RestoreCustomHooksServer) error {
	return nil
}

func (m *mockRepoSvc) BackupCustomHooks(*gitalypb.BackupCustomHooksRequest, gitalypb.RepositoryService_BackupCustomHooksServer) error {
	return nil
}
