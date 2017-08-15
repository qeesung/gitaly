package ref

import (
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
)

func TestRefExists(t *testing.T) {
	type args struct {
		ctx context.Context
		in  *pb.RefExistsRequest
	}
	tests := []struct {
		name    string
		ref     string
		want    bool
		wantErr codes.Code
	}{
		{"master", "refs/heads/master", true, codes.OK},
		{"v1.0.0", "refs/tags/v1.0.0", true, codes.OK},
		{"unicode exists", "refs/heads/ʕ•ᴥ•ʔ", true, codes.OK},
		{"unicode missing", "refs/tags/अस्तित्वहीन", false, codes.OK},
		{"spaces", "refs/ /heads", false, codes.OK},
		{"haxxors", "refs/; rm -rf /tmp/*", false, codes.OK},
		{"dashes", "--", false, codes.InvalidArgument},
		{"blank", "", false, codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := runRefServiceServer(t)
			defer server.Stop()

			client, conn := newRefClient(t)
			defer conn.Close()

			req := &pb.RefExistsRequest{Repository: testRepo, Ref: []byte(tt.ref)}

			got, err := client.RefExists(context.Background(), req)

			if grpc.Code(err) != tt.wantErr {
				t.Errorf("server.RefExists() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr != codes.OK {
				if got != nil {
					t.Errorf("server.RefExists() = %v, want null", got)
				}
				return
			}

			if got.Value != tt.want {
				t.Errorf("server.RefExists() = %v, want %v", got.Value, tt.want)
			}
		})
	}
}
