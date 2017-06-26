package renameadapter

import pb "gitlab.com/gitlab-org/gitaly-proto/go"

type smartHTTPAdapter struct {
	upstream pb.SmartHTTPServiceServer
}

func (s *smartHTTPAdapter) InfoRefsUploadPack(in *pb.InfoRefsRequest, stream pb.SmartHTTP_InfoRefsUploadPackServer) error {
	return s.upstream.InfoRefsUploadPack(in, stream)
}

func (s *smartHTTPAdapter) InfoRefsReceivePack(in *pb.InfoRefsRequest, stream pb.SmartHTTP_InfoRefsReceivePackServer) error {
	return s.upstream.InfoRefsReceivePack(in, stream)
}

func (s *smartHTTPAdapter) PostUploadPack(stream pb.SmartHTTP_PostUploadPackServer) error {
	return s.upstream.PostUploadPack(stream)
}

func (s *smartHTTPAdapter) PostReceivePack(stream pb.SmartHTTP_PostReceivePackServer) error {
	return s.upstream.PostReceivePack(stream)
}

// NewSmartHTTPAdapter creates an adapter between SmartHTTPServiceServer and SmartHTTPServer
func NewSmartHTTPAdapter(upstream pb.SmartHTTPServiceServer) pb.SmartHTTPServer {
	return &smartHTTPAdapter{upstream}
}
