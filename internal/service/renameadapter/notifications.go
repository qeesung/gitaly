package renameadapter

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
)

type notificationAdapter struct {
	upstream pb.NotificationServiceServer
}

func (s *notificationAdapter) PostReceive(ctx context.Context, req *pb.PostReceiveRequest) (*pb.PostReceiveResponse, error) {
	return s.upstream.PostReceive(ctx, req)
}

// NewNotificationAdapter adapts NotificationServiceServer to NotificationsServer
func NewNotificationAdapter(upstream pb.NotificationServiceServer) pb.NotificationsServer {
	return &notificationAdapter{upstream}
}
