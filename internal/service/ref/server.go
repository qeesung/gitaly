package ref

import pb "gitlab.com/gitlab-org/gitaly-proto/go"

const maxMsgSize = 1024

type server struct {
	MaxMsgSize int
}

// NewServer creates a new instance of a grpc RefServer
func NewServer() pb.RefServiceServer {
	return &server{MaxMsgSize: maxMsgSize}
}
