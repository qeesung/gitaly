package ssh

import pb "gitlab.com/gitlab-org/gitaly-proto/go"

type bridge struct {
	upstream pb.SSHServiceServer
}

func (s *bridge) SSHUploadPack(stream pb.SSH_SSHUploadPackServer) error {
	return s.upstream.SSHUploadPack(stream)
}

func (s *bridge) SSHReceivePack(stream pb.SSH_SSHReceivePackServer) error {
	return s.upstream.SSHReceivePack(stream)
}

// NewRenameBridge creates a bridge between SmartHTTPServiceServer and SmartHTTPServer
func NewRenameBridge(upstream pb.SSHServiceServer) pb.SSHServer {
	return &bridge{upstream}
}
