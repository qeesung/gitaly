package repository

import (
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"

	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

func writeRefReqWithStorage(req gitalypb.WriteRefRequest, storage string) *gitalypb.WriteRefRequest {
	req.Repository = &gitalypb.Repository{
		StorageName:  storage,
		RelativePath: req.GetRepository().GetRelativePath(),
	}
	return &req
}

func (s *Server) WriteRef(srv interface{}, stream grpc.ServerStream) error {
	var writeRefReq gitalypb.WriteRefRequest

	if err := stream.RecvMsg(&writeRefReq); err != nil {
		return err
	}

	shard, err := s.nodeManager.GetShard(writeRefReq.GetRepository().GetStorageName())
	if err != nil {
		return err
	}

	primary, err := shard.GetPrimary()
	if err != nil {
		return err
	}

	secondaries, err := shard.GetSecondaries()
	if err != nil {
		return err
	}

	if _, err := gitalypb.NewRepositoryServiceClient(
		primary.GetConnection()).WriteRef(stream.Context(),
		writeRefReqWithStorage(writeRefReq, primary.GetStorage()),
	); err != nil {
		return err
	}

	failedNodeStorages := make(map[string]struct{})
	var wg sync.WaitGroup
	wg.Add(len(secondaries))

	for _, secondary := range secondaries {
		go func(secondary nodes.Node) {
			defer wg.Done()
			client := gitalypb.NewRepositoryServiceClient(secondary.GetConnection())

			if _, err := client.WriteRef(stream.Context(), writeRefReqWithStorage(writeRefReq, secondary.GetStorage())); err != nil {
				failedNodeStorages[secondary.GetStorage()] = struct{}{}
			}
		}(secondary)
	}
	wg.Wait()

	var failedSecondaries []string
	for storage, _ := range failedNodeStorages {
		failedSecondaries = append(failedSecondaries, storage)
	}

	if _, err := s.ds.CreateReplicaReplJobs(writeRefReq.GetRepository().GetRelativePath(), primary.GetStorage(), failedSecondaries, datastore.UpdateRepo, nil); err != nil {
		return err
	}

	return stream.SendMsg(&gitalypb.WriteRefResponse{})
}
