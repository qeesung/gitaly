package smarthttp

import pb "gitlab.com/gitlab-org/gitaly-proto/go"

type bridge struct {
	upstream pb.SmartHTTPServiceServer
}

func (s *bridge) InfoRefsUploadPack(in *pb.InfoRefsRequest, stream pb.SmartHTTP_InfoRefsUploadPackServer) error {
	return s.upstream.InfoRefsReceivePack(in, stream)
}

func (s *bridge) InfoRefsReceivePack(in *pb.InfoRefsRequest, stream pb.SmartHTTP_InfoRefsReceivePackServer) error {
	return s.upstream.InfoRefsReceivePack(in, stream)
}

func (s *bridge) PostUploadPack(stream pb.SmartHTTP_PostUploadPackServer) error {
	return s.upstream.PostUploadPack(stream)
}

func (s *bridge) PostReceivePack(stream pb.SmartHTTP_PostReceivePackServer) error {
	return s.upstream.PostReceivePack(stream)
}

// NewRenameBridge creates a bridge between SmartHTTPServiceServer and SmartHTTPServer
func NewRenameBridge(upstream pb.SmartHTTPServiceServer) pb.SmartHTTPServer {
	return &bridge{upstream}
}
