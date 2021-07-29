// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package gitalypb

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// RefServiceClient is the client API for RefService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type RefServiceClient interface {
	FindDefaultBranchName(ctx context.Context, in *FindDefaultBranchNameRequest, opts ...grpc.CallOption) (*FindDefaultBranchNameResponse, error)
	FindAllBranchNames(ctx context.Context, in *FindAllBranchNamesRequest, opts ...grpc.CallOption) (RefService_FindAllBranchNamesClient, error)
	FindAllTagNames(ctx context.Context, in *FindAllTagNamesRequest, opts ...grpc.CallOption) (RefService_FindAllTagNamesClient, error)
	// Find a Ref matching the given constraints. Response may be empty.
	FindRefName(ctx context.Context, in *FindRefNameRequest, opts ...grpc.CallOption) (*FindRefNameResponse, error)
	// Return a stream so we can divide the response in chunks of branches
	FindLocalBranches(ctx context.Context, in *FindLocalBranchesRequest, opts ...grpc.CallOption) (RefService_FindLocalBranchesClient, error)
	FindAllBranches(ctx context.Context, in *FindAllBranchesRequest, opts ...grpc.CallOption) (RefService_FindAllBranchesClient, error)
	// Returns a stream of tags repository has.
	FindAllTags(ctx context.Context, in *FindAllTagsRequest, opts ...grpc.CallOption) (RefService_FindAllTagsClient, error)
	FindTag(ctx context.Context, in *FindTagRequest, opts ...grpc.CallOption) (*FindTagResponse, error)
	FindAllRemoteBranches(ctx context.Context, in *FindAllRemoteBranchesRequest, opts ...grpc.CallOption) (RefService_FindAllRemoteBranchesClient, error)
	RefExists(ctx context.Context, in *RefExistsRequest, opts ...grpc.CallOption) (*RefExistsResponse, error)
	// FindBranch finds a branch by its unqualified name (like "master") and
	// returns the commit it currently points to.
	FindBranch(ctx context.Context, in *FindBranchRequest, opts ...grpc.CallOption) (*FindBranchResponse, error)
	DeleteRefs(ctx context.Context, in *DeleteRefsRequest, opts ...grpc.CallOption) (*DeleteRefsResponse, error)
	ListBranchNamesContainingCommit(ctx context.Context, in *ListBranchNamesContainingCommitRequest, opts ...grpc.CallOption) (RefService_ListBranchNamesContainingCommitClient, error)
	ListTagNamesContainingCommit(ctx context.Context, in *ListTagNamesContainingCommitRequest, opts ...grpc.CallOption) (RefService_ListTagNamesContainingCommitClient, error)
	// GetTagSignatures returns signatures for annotated tags resolved from a set of revisions. Revisions
	// which don't resolve to an annotated tag are silently discarded. Revisions which cannot be resolved
	// result in an error. Tags which are annotated but not signed will return a TagSignature response
	// which has no signature, but its unsigned contents will still be returned.
	GetTagSignatures(ctx context.Context, in *GetTagSignaturesRequest, opts ...grpc.CallOption) (RefService_GetTagSignaturesClient, error)
	GetTagMessages(ctx context.Context, in *GetTagMessagesRequest, opts ...grpc.CallOption) (RefService_GetTagMessagesClient, error)
	// Returns commits that are only reachable from the ref passed
	ListNewCommits(ctx context.Context, in *ListNewCommitsRequest, opts ...grpc.CallOption) (RefService_ListNewCommitsClient, error)
	ListNewBlobs(ctx context.Context, in *ListNewBlobsRequest, opts ...grpc.CallOption) (RefService_ListNewBlobsClient, error)
	PackRefs(ctx context.Context, in *PackRefsRequest, opts ...grpc.CallOption) (*PackRefsResponse, error)
}

type refServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewRefServiceClient(cc grpc.ClientConnInterface) RefServiceClient {
	return &refServiceClient{cc}
}

func (c *refServiceClient) FindDefaultBranchName(ctx context.Context, in *FindDefaultBranchNameRequest, opts ...grpc.CallOption) (*FindDefaultBranchNameResponse, error) {
	out := new(FindDefaultBranchNameResponse)
	err := c.cc.Invoke(ctx, "/gitaly.RefService/FindDefaultBranchName", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *refServiceClient) FindAllBranchNames(ctx context.Context, in *FindAllBranchNamesRequest, opts ...grpc.CallOption) (RefService_FindAllBranchNamesClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[0], "/gitaly.RefService/FindAllBranchNames", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceFindAllBranchNamesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_FindAllBranchNamesClient interface {
	Recv() (*FindAllBranchNamesResponse, error)
	grpc.ClientStream
}

type refServiceFindAllBranchNamesClient struct {
	grpc.ClientStream
}

func (x *refServiceFindAllBranchNamesClient) Recv() (*FindAllBranchNamesResponse, error) {
	m := new(FindAllBranchNamesResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) FindAllTagNames(ctx context.Context, in *FindAllTagNamesRequest, opts ...grpc.CallOption) (RefService_FindAllTagNamesClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[1], "/gitaly.RefService/FindAllTagNames", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceFindAllTagNamesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_FindAllTagNamesClient interface {
	Recv() (*FindAllTagNamesResponse, error)
	grpc.ClientStream
}

type refServiceFindAllTagNamesClient struct {
	grpc.ClientStream
}

func (x *refServiceFindAllTagNamesClient) Recv() (*FindAllTagNamesResponse, error) {
	m := new(FindAllTagNamesResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) FindRefName(ctx context.Context, in *FindRefNameRequest, opts ...grpc.CallOption) (*FindRefNameResponse, error) {
	out := new(FindRefNameResponse)
	err := c.cc.Invoke(ctx, "/gitaly.RefService/FindRefName", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *refServiceClient) FindLocalBranches(ctx context.Context, in *FindLocalBranchesRequest, opts ...grpc.CallOption) (RefService_FindLocalBranchesClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[2], "/gitaly.RefService/FindLocalBranches", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceFindLocalBranchesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_FindLocalBranchesClient interface {
	Recv() (*FindLocalBranchesResponse, error)
	grpc.ClientStream
}

type refServiceFindLocalBranchesClient struct {
	grpc.ClientStream
}

func (x *refServiceFindLocalBranchesClient) Recv() (*FindLocalBranchesResponse, error) {
	m := new(FindLocalBranchesResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) FindAllBranches(ctx context.Context, in *FindAllBranchesRequest, opts ...grpc.CallOption) (RefService_FindAllBranchesClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[3], "/gitaly.RefService/FindAllBranches", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceFindAllBranchesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_FindAllBranchesClient interface {
	Recv() (*FindAllBranchesResponse, error)
	grpc.ClientStream
}

type refServiceFindAllBranchesClient struct {
	grpc.ClientStream
}

func (x *refServiceFindAllBranchesClient) Recv() (*FindAllBranchesResponse, error) {
	m := new(FindAllBranchesResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) FindAllTags(ctx context.Context, in *FindAllTagsRequest, opts ...grpc.CallOption) (RefService_FindAllTagsClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[4], "/gitaly.RefService/FindAllTags", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceFindAllTagsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_FindAllTagsClient interface {
	Recv() (*FindAllTagsResponse, error)
	grpc.ClientStream
}

type refServiceFindAllTagsClient struct {
	grpc.ClientStream
}

func (x *refServiceFindAllTagsClient) Recv() (*FindAllTagsResponse, error) {
	m := new(FindAllTagsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) FindTag(ctx context.Context, in *FindTagRequest, opts ...grpc.CallOption) (*FindTagResponse, error) {
	out := new(FindTagResponse)
	err := c.cc.Invoke(ctx, "/gitaly.RefService/FindTag", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *refServiceClient) FindAllRemoteBranches(ctx context.Context, in *FindAllRemoteBranchesRequest, opts ...grpc.CallOption) (RefService_FindAllRemoteBranchesClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[5], "/gitaly.RefService/FindAllRemoteBranches", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceFindAllRemoteBranchesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_FindAllRemoteBranchesClient interface {
	Recv() (*FindAllRemoteBranchesResponse, error)
	grpc.ClientStream
}

type refServiceFindAllRemoteBranchesClient struct {
	grpc.ClientStream
}

func (x *refServiceFindAllRemoteBranchesClient) Recv() (*FindAllRemoteBranchesResponse, error) {
	m := new(FindAllRemoteBranchesResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) RefExists(ctx context.Context, in *RefExistsRequest, opts ...grpc.CallOption) (*RefExistsResponse, error) {
	out := new(RefExistsResponse)
	err := c.cc.Invoke(ctx, "/gitaly.RefService/RefExists", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *refServiceClient) FindBranch(ctx context.Context, in *FindBranchRequest, opts ...grpc.CallOption) (*FindBranchResponse, error) {
	out := new(FindBranchResponse)
	err := c.cc.Invoke(ctx, "/gitaly.RefService/FindBranch", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *refServiceClient) DeleteRefs(ctx context.Context, in *DeleteRefsRequest, opts ...grpc.CallOption) (*DeleteRefsResponse, error) {
	out := new(DeleteRefsResponse)
	err := c.cc.Invoke(ctx, "/gitaly.RefService/DeleteRefs", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *refServiceClient) ListBranchNamesContainingCommit(ctx context.Context, in *ListBranchNamesContainingCommitRequest, opts ...grpc.CallOption) (RefService_ListBranchNamesContainingCommitClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[6], "/gitaly.RefService/ListBranchNamesContainingCommit", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceListBranchNamesContainingCommitClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_ListBranchNamesContainingCommitClient interface {
	Recv() (*ListBranchNamesContainingCommitResponse, error)
	grpc.ClientStream
}

type refServiceListBranchNamesContainingCommitClient struct {
	grpc.ClientStream
}

func (x *refServiceListBranchNamesContainingCommitClient) Recv() (*ListBranchNamesContainingCommitResponse, error) {
	m := new(ListBranchNamesContainingCommitResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) ListTagNamesContainingCommit(ctx context.Context, in *ListTagNamesContainingCommitRequest, opts ...grpc.CallOption) (RefService_ListTagNamesContainingCommitClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[7], "/gitaly.RefService/ListTagNamesContainingCommit", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceListTagNamesContainingCommitClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_ListTagNamesContainingCommitClient interface {
	Recv() (*ListTagNamesContainingCommitResponse, error)
	grpc.ClientStream
}

type refServiceListTagNamesContainingCommitClient struct {
	grpc.ClientStream
}

func (x *refServiceListTagNamesContainingCommitClient) Recv() (*ListTagNamesContainingCommitResponse, error) {
	m := new(ListTagNamesContainingCommitResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) GetTagSignatures(ctx context.Context, in *GetTagSignaturesRequest, opts ...grpc.CallOption) (RefService_GetTagSignaturesClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[8], "/gitaly.RefService/GetTagSignatures", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceGetTagSignaturesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_GetTagSignaturesClient interface {
	Recv() (*GetTagSignaturesResponse, error)
	grpc.ClientStream
}

type refServiceGetTagSignaturesClient struct {
	grpc.ClientStream
}

func (x *refServiceGetTagSignaturesClient) Recv() (*GetTagSignaturesResponse, error) {
	m := new(GetTagSignaturesResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) GetTagMessages(ctx context.Context, in *GetTagMessagesRequest, opts ...grpc.CallOption) (RefService_GetTagMessagesClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[9], "/gitaly.RefService/GetTagMessages", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceGetTagMessagesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_GetTagMessagesClient interface {
	Recv() (*GetTagMessagesResponse, error)
	grpc.ClientStream
}

type refServiceGetTagMessagesClient struct {
	grpc.ClientStream
}

func (x *refServiceGetTagMessagesClient) Recv() (*GetTagMessagesResponse, error) {
	m := new(GetTagMessagesResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) ListNewCommits(ctx context.Context, in *ListNewCommitsRequest, opts ...grpc.CallOption) (RefService_ListNewCommitsClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[10], "/gitaly.RefService/ListNewCommits", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceListNewCommitsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_ListNewCommitsClient interface {
	Recv() (*ListNewCommitsResponse, error)
	grpc.ClientStream
}

type refServiceListNewCommitsClient struct {
	grpc.ClientStream
}

func (x *refServiceListNewCommitsClient) Recv() (*ListNewCommitsResponse, error) {
	m := new(ListNewCommitsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) ListNewBlobs(ctx context.Context, in *ListNewBlobsRequest, opts ...grpc.CallOption) (RefService_ListNewBlobsClient, error) {
	stream, err := c.cc.NewStream(ctx, &RefService_ServiceDesc.Streams[11], "/gitaly.RefService/ListNewBlobs", opts...)
	if err != nil {
		return nil, err
	}
	x := &refServiceListNewBlobsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type RefService_ListNewBlobsClient interface {
	Recv() (*ListNewBlobsResponse, error)
	grpc.ClientStream
}

type refServiceListNewBlobsClient struct {
	grpc.ClientStream
}

func (x *refServiceListNewBlobsClient) Recv() (*ListNewBlobsResponse, error) {
	m := new(ListNewBlobsResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *refServiceClient) PackRefs(ctx context.Context, in *PackRefsRequest, opts ...grpc.CallOption) (*PackRefsResponse, error) {
	out := new(PackRefsResponse)
	err := c.cc.Invoke(ctx, "/gitaly.RefService/PackRefs", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RefServiceServer is the server API for RefService service.
// All implementations must embed UnimplementedRefServiceServer
// for forward compatibility
type RefServiceServer interface {
	FindDefaultBranchName(context.Context, *FindDefaultBranchNameRequest) (*FindDefaultBranchNameResponse, error)
	FindAllBranchNames(*FindAllBranchNamesRequest, RefService_FindAllBranchNamesServer) error
	FindAllTagNames(*FindAllTagNamesRequest, RefService_FindAllTagNamesServer) error
	// Find a Ref matching the given constraints. Response may be empty.
	FindRefName(context.Context, *FindRefNameRequest) (*FindRefNameResponse, error)
	// Return a stream so we can divide the response in chunks of branches
	FindLocalBranches(*FindLocalBranchesRequest, RefService_FindLocalBranchesServer) error
	FindAllBranches(*FindAllBranchesRequest, RefService_FindAllBranchesServer) error
	// Returns a stream of tags repository has.
	FindAllTags(*FindAllTagsRequest, RefService_FindAllTagsServer) error
	FindTag(context.Context, *FindTagRequest) (*FindTagResponse, error)
	FindAllRemoteBranches(*FindAllRemoteBranchesRequest, RefService_FindAllRemoteBranchesServer) error
	RefExists(context.Context, *RefExistsRequest) (*RefExistsResponse, error)
	// FindBranch finds a branch by its unqualified name (like "master") and
	// returns the commit it currently points to.
	FindBranch(context.Context, *FindBranchRequest) (*FindBranchResponse, error)
	DeleteRefs(context.Context, *DeleteRefsRequest) (*DeleteRefsResponse, error)
	ListBranchNamesContainingCommit(*ListBranchNamesContainingCommitRequest, RefService_ListBranchNamesContainingCommitServer) error
	ListTagNamesContainingCommit(*ListTagNamesContainingCommitRequest, RefService_ListTagNamesContainingCommitServer) error
	// GetTagSignatures returns signatures for annotated tags resolved from a set of revisions. Revisions
	// which don't resolve to an annotated tag are silently discarded. Revisions which cannot be resolved
	// result in an error. Tags which are annotated but not signed will return a TagSignature response
	// which has no signature, but its unsigned contents will still be returned.
	GetTagSignatures(*GetTagSignaturesRequest, RefService_GetTagSignaturesServer) error
	GetTagMessages(*GetTagMessagesRequest, RefService_GetTagMessagesServer) error
	// Returns commits that are only reachable from the ref passed
	ListNewCommits(*ListNewCommitsRequest, RefService_ListNewCommitsServer) error
	ListNewBlobs(*ListNewBlobsRequest, RefService_ListNewBlobsServer) error
	PackRefs(context.Context, *PackRefsRequest) (*PackRefsResponse, error)
	mustEmbedUnimplementedRefServiceServer()
}

// UnimplementedRefServiceServer must be embedded to have forward compatible implementations.
type UnimplementedRefServiceServer struct {
}

func (UnimplementedRefServiceServer) FindDefaultBranchName(context.Context, *FindDefaultBranchNameRequest) (*FindDefaultBranchNameResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method FindDefaultBranchName not implemented")
}
func (UnimplementedRefServiceServer) FindAllBranchNames(*FindAllBranchNamesRequest, RefService_FindAllBranchNamesServer) error {
	return status.Errorf(codes.Unimplemented, "method FindAllBranchNames not implemented")
}
func (UnimplementedRefServiceServer) FindAllTagNames(*FindAllTagNamesRequest, RefService_FindAllTagNamesServer) error {
	return status.Errorf(codes.Unimplemented, "method FindAllTagNames not implemented")
}
func (UnimplementedRefServiceServer) FindRefName(context.Context, *FindRefNameRequest) (*FindRefNameResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method FindRefName not implemented")
}
func (UnimplementedRefServiceServer) FindLocalBranches(*FindLocalBranchesRequest, RefService_FindLocalBranchesServer) error {
	return status.Errorf(codes.Unimplemented, "method FindLocalBranches not implemented")
}
func (UnimplementedRefServiceServer) FindAllBranches(*FindAllBranchesRequest, RefService_FindAllBranchesServer) error {
	return status.Errorf(codes.Unimplemented, "method FindAllBranches not implemented")
}
func (UnimplementedRefServiceServer) FindAllTags(*FindAllTagsRequest, RefService_FindAllTagsServer) error {
	return status.Errorf(codes.Unimplemented, "method FindAllTags not implemented")
}
func (UnimplementedRefServiceServer) FindTag(context.Context, *FindTagRequest) (*FindTagResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method FindTag not implemented")
}
func (UnimplementedRefServiceServer) FindAllRemoteBranches(*FindAllRemoteBranchesRequest, RefService_FindAllRemoteBranchesServer) error {
	return status.Errorf(codes.Unimplemented, "method FindAllRemoteBranches not implemented")
}
func (UnimplementedRefServiceServer) RefExists(context.Context, *RefExistsRequest) (*RefExistsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RefExists not implemented")
}
func (UnimplementedRefServiceServer) FindBranch(context.Context, *FindBranchRequest) (*FindBranchResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method FindBranch not implemented")
}
func (UnimplementedRefServiceServer) DeleteRefs(context.Context, *DeleteRefsRequest) (*DeleteRefsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DeleteRefs not implemented")
}
func (UnimplementedRefServiceServer) ListBranchNamesContainingCommit(*ListBranchNamesContainingCommitRequest, RefService_ListBranchNamesContainingCommitServer) error {
	return status.Errorf(codes.Unimplemented, "method ListBranchNamesContainingCommit not implemented")
}
func (UnimplementedRefServiceServer) ListTagNamesContainingCommit(*ListTagNamesContainingCommitRequest, RefService_ListTagNamesContainingCommitServer) error {
	return status.Errorf(codes.Unimplemented, "method ListTagNamesContainingCommit not implemented")
}
func (UnimplementedRefServiceServer) GetTagSignatures(*GetTagSignaturesRequest, RefService_GetTagSignaturesServer) error {
	return status.Errorf(codes.Unimplemented, "method GetTagSignatures not implemented")
}
func (UnimplementedRefServiceServer) GetTagMessages(*GetTagMessagesRequest, RefService_GetTagMessagesServer) error {
	return status.Errorf(codes.Unimplemented, "method GetTagMessages not implemented")
}
func (UnimplementedRefServiceServer) ListNewCommits(*ListNewCommitsRequest, RefService_ListNewCommitsServer) error {
	return status.Errorf(codes.Unimplemented, "method ListNewCommits not implemented")
}
func (UnimplementedRefServiceServer) ListNewBlobs(*ListNewBlobsRequest, RefService_ListNewBlobsServer) error {
	return status.Errorf(codes.Unimplemented, "method ListNewBlobs not implemented")
}
func (UnimplementedRefServiceServer) PackRefs(context.Context, *PackRefsRequest) (*PackRefsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method PackRefs not implemented")
}
func (UnimplementedRefServiceServer) mustEmbedUnimplementedRefServiceServer() {}

// UnsafeRefServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to RefServiceServer will
// result in compilation errors.
type UnsafeRefServiceServer interface {
	mustEmbedUnimplementedRefServiceServer()
}

func RegisterRefServiceServer(s grpc.ServiceRegistrar, srv RefServiceServer) {
	s.RegisterService(&RefService_ServiceDesc, srv)
}

func _RefService_FindDefaultBranchName_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(FindDefaultBranchNameRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RefServiceServer).FindDefaultBranchName(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.RefService/FindDefaultBranchName",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RefServiceServer).FindDefaultBranchName(ctx, req.(*FindDefaultBranchNameRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _RefService_FindAllBranchNames_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(FindAllBranchNamesRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).FindAllBranchNames(m, &refServiceFindAllBranchNamesServer{stream})
}

type RefService_FindAllBranchNamesServer interface {
	Send(*FindAllBranchNamesResponse) error
	grpc.ServerStream
}

type refServiceFindAllBranchNamesServer struct {
	grpc.ServerStream
}

func (x *refServiceFindAllBranchNamesServer) Send(m *FindAllBranchNamesResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_FindAllTagNames_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(FindAllTagNamesRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).FindAllTagNames(m, &refServiceFindAllTagNamesServer{stream})
}

type RefService_FindAllTagNamesServer interface {
	Send(*FindAllTagNamesResponse) error
	grpc.ServerStream
}

type refServiceFindAllTagNamesServer struct {
	grpc.ServerStream
}

func (x *refServiceFindAllTagNamesServer) Send(m *FindAllTagNamesResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_FindRefName_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(FindRefNameRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RefServiceServer).FindRefName(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.RefService/FindRefName",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RefServiceServer).FindRefName(ctx, req.(*FindRefNameRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _RefService_FindLocalBranches_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(FindLocalBranchesRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).FindLocalBranches(m, &refServiceFindLocalBranchesServer{stream})
}

type RefService_FindLocalBranchesServer interface {
	Send(*FindLocalBranchesResponse) error
	grpc.ServerStream
}

type refServiceFindLocalBranchesServer struct {
	grpc.ServerStream
}

func (x *refServiceFindLocalBranchesServer) Send(m *FindLocalBranchesResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_FindAllBranches_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(FindAllBranchesRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).FindAllBranches(m, &refServiceFindAllBranchesServer{stream})
}

type RefService_FindAllBranchesServer interface {
	Send(*FindAllBranchesResponse) error
	grpc.ServerStream
}

type refServiceFindAllBranchesServer struct {
	grpc.ServerStream
}

func (x *refServiceFindAllBranchesServer) Send(m *FindAllBranchesResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_FindAllTags_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(FindAllTagsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).FindAllTags(m, &refServiceFindAllTagsServer{stream})
}

type RefService_FindAllTagsServer interface {
	Send(*FindAllTagsResponse) error
	grpc.ServerStream
}

type refServiceFindAllTagsServer struct {
	grpc.ServerStream
}

func (x *refServiceFindAllTagsServer) Send(m *FindAllTagsResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_FindTag_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(FindTagRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RefServiceServer).FindTag(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.RefService/FindTag",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RefServiceServer).FindTag(ctx, req.(*FindTagRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _RefService_FindAllRemoteBranches_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(FindAllRemoteBranchesRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).FindAllRemoteBranches(m, &refServiceFindAllRemoteBranchesServer{stream})
}

type RefService_FindAllRemoteBranchesServer interface {
	Send(*FindAllRemoteBranchesResponse) error
	grpc.ServerStream
}

type refServiceFindAllRemoteBranchesServer struct {
	grpc.ServerStream
}

func (x *refServiceFindAllRemoteBranchesServer) Send(m *FindAllRemoteBranchesResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_RefExists_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RefExistsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RefServiceServer).RefExists(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.RefService/RefExists",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RefServiceServer).RefExists(ctx, req.(*RefExistsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _RefService_FindBranch_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(FindBranchRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RefServiceServer).FindBranch(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.RefService/FindBranch",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RefServiceServer).FindBranch(ctx, req.(*FindBranchRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _RefService_DeleteRefs_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteRefsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RefServiceServer).DeleteRefs(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.RefService/DeleteRefs",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RefServiceServer).DeleteRefs(ctx, req.(*DeleteRefsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _RefService_ListBranchNamesContainingCommit_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(ListBranchNamesContainingCommitRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).ListBranchNamesContainingCommit(m, &refServiceListBranchNamesContainingCommitServer{stream})
}

type RefService_ListBranchNamesContainingCommitServer interface {
	Send(*ListBranchNamesContainingCommitResponse) error
	grpc.ServerStream
}

type refServiceListBranchNamesContainingCommitServer struct {
	grpc.ServerStream
}

func (x *refServiceListBranchNamesContainingCommitServer) Send(m *ListBranchNamesContainingCommitResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_ListTagNamesContainingCommit_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(ListTagNamesContainingCommitRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).ListTagNamesContainingCommit(m, &refServiceListTagNamesContainingCommitServer{stream})
}

type RefService_ListTagNamesContainingCommitServer interface {
	Send(*ListTagNamesContainingCommitResponse) error
	grpc.ServerStream
}

type refServiceListTagNamesContainingCommitServer struct {
	grpc.ServerStream
}

func (x *refServiceListTagNamesContainingCommitServer) Send(m *ListTagNamesContainingCommitResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_GetTagSignatures_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(GetTagSignaturesRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).GetTagSignatures(m, &refServiceGetTagSignaturesServer{stream})
}

type RefService_GetTagSignaturesServer interface {
	Send(*GetTagSignaturesResponse) error
	grpc.ServerStream
}

type refServiceGetTagSignaturesServer struct {
	grpc.ServerStream
}

func (x *refServiceGetTagSignaturesServer) Send(m *GetTagSignaturesResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_GetTagMessages_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(GetTagMessagesRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).GetTagMessages(m, &refServiceGetTagMessagesServer{stream})
}

type RefService_GetTagMessagesServer interface {
	Send(*GetTagMessagesResponse) error
	grpc.ServerStream
}

type refServiceGetTagMessagesServer struct {
	grpc.ServerStream
}

func (x *refServiceGetTagMessagesServer) Send(m *GetTagMessagesResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_ListNewCommits_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(ListNewCommitsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).ListNewCommits(m, &refServiceListNewCommitsServer{stream})
}

type RefService_ListNewCommitsServer interface {
	Send(*ListNewCommitsResponse) error
	grpc.ServerStream
}

type refServiceListNewCommitsServer struct {
	grpc.ServerStream
}

func (x *refServiceListNewCommitsServer) Send(m *ListNewCommitsResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_ListNewBlobs_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(ListNewBlobsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(RefServiceServer).ListNewBlobs(m, &refServiceListNewBlobsServer{stream})
}

type RefService_ListNewBlobsServer interface {
	Send(*ListNewBlobsResponse) error
	grpc.ServerStream
}

type refServiceListNewBlobsServer struct {
	grpc.ServerStream
}

func (x *refServiceListNewBlobsServer) Send(m *ListNewBlobsResponse) error {
	return x.ServerStream.SendMsg(m)
}

func _RefService_PackRefs_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PackRefsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RefServiceServer).PackRefs(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gitaly.RefService/PackRefs",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RefServiceServer).PackRefs(ctx, req.(*PackRefsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// RefService_ServiceDesc is the grpc.ServiceDesc for RefService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var RefService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gitaly.RefService",
	HandlerType: (*RefServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "FindDefaultBranchName",
			Handler:    _RefService_FindDefaultBranchName_Handler,
		},
		{
			MethodName: "FindRefName",
			Handler:    _RefService_FindRefName_Handler,
		},
		{
			MethodName: "FindTag",
			Handler:    _RefService_FindTag_Handler,
		},
		{
			MethodName: "RefExists",
			Handler:    _RefService_RefExists_Handler,
		},
		{
			MethodName: "FindBranch",
			Handler:    _RefService_FindBranch_Handler,
		},
		{
			MethodName: "DeleteRefs",
			Handler:    _RefService_DeleteRefs_Handler,
		},
		{
			MethodName: "PackRefs",
			Handler:    _RefService_PackRefs_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "FindAllBranchNames",
			Handler:       _RefService_FindAllBranchNames_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "FindAllTagNames",
			Handler:       _RefService_FindAllTagNames_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "FindLocalBranches",
			Handler:       _RefService_FindLocalBranches_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "FindAllBranches",
			Handler:       _RefService_FindAllBranches_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "FindAllTags",
			Handler:       _RefService_FindAllTags_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "FindAllRemoteBranches",
			Handler:       _RefService_FindAllRemoteBranches_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "ListBranchNamesContainingCommit",
			Handler:       _RefService_ListBranchNamesContainingCommit_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "ListTagNamesContainingCommit",
			Handler:       _RefService_ListTagNamesContainingCommit_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "GetTagSignatures",
			Handler:       _RefService_GetTagSignatures_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "GetTagMessages",
			Handler:       _RefService_GetTagMessages_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "ListNewCommits",
			Handler:       _RefService_ListNewCommits_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "ListNewBlobs",
			Handler:       _RefService_ListNewBlobs_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "ref.proto",
}
