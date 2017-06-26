package renameadapter

import pb "gitlab.com/gitlab-org/gitaly-proto/go"

type sshAdapter struct {
	upstream pb.SSHServiceServer
}

func (s *sshAdapter) SSHUploadPack(stream pb.SSH_SSHUploadPackServer) error {
	return s.upstream.SSHUploadPack(stream)
}

func (s *sshAdapter) SSHReceivePack(stream pb.SSH_SSHReceivePackServer) error {
	return s.upstream.SSHReceivePack(stream)
}

// NewSSHAdapter creates a sshAdapter between SmartHTTPServiceServer and SmartHTTPServer
func NewSSHAdapter(upstream pb.SSHServiceServer) pb.SSHServer {
	return &sshAdapter{upstream}
}
