package server

import (
	"fmt"
	"io"
	"io/ioutil"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

func (s *server) GetObjects(in *gitalypb.GetObjectsRequest, stream gitalypb.ServerService_GetObjectsServer) error {
	if err := validateGetObjectsRequest(in); err != nil {
		return status.Errorf(codes.InvalidArgument, "GetObjects: %v", err)
	}

	c, err := catfile.New(stream.Context(), in.Repository)
	if err != nil {
		return status.Errorf(codes.Internal, "GetObjects: %v", err)
	}

	for _, oid := range in.Oids {
		objectInfo, err := c.Info(oid)
		if err != nil && !catfile.IsNotFound(err) {
			return status.Errorf(codes.Internal, "GetObjects: %v", err)
		}
		if catfile.IsNotFound(err) {
			logrus.WithField("oid", oid).Error("not found oid")
		}
		response := &gitalypb.GetObjectsResponse{
			Size: objectInfo.Size,
			Oid:  objectInfo.Oid,
			Type: lookupType(objectInfo.Type),
		}
		reader, err := c.Object(objectInfo.Oid, objectInfo.Type)
		if err != nil {
			return status.Errorf(codes.Internal, "GetObjects: %v", err)
		}

		sw := streamio.NewWriter(func(p []byte) error {
			msg := &gitalypb.GetObjectsResponse{}
			if response != nil {
				msg = response
				response = nil
			}

			msg.Data = p

			return stream.Send(msg)
		})

		_, err = io.Copy(sw, reader)
		if err != nil {
			return status.Errorf(codes.Unavailable, "GetObjects: send: %v", err)
		}

		if _, err := io.Copy(ioutil.Discard, reader); err != nil {
			return status.Errorf(codes.Unavailable, "GetObjects: discarding data: %v", err)
		}

	}

	return nil
}

func validateGetObjectsRequest(in *gitalypb.GetObjectsRequest) error {
	if len(in.GetOids()) == 0 {
		return fmt.Errorf("empty Oids")
	}
	return nil
}

func lookupType(infoType string) gitalypb.ObjectType {
	switch infoType {
	case "commit":
		return gitalypb.ObjectType_COMMIT
	case "blob":
		return gitalypb.ObjectType_BLOB
	case "tree":
		return gitalypb.ObjectType_TREE
	case "tag":
		return gitalypb.ObjectType_TAG
	default:
		return gitalypb.ObjectType_UNKNOWN
	}
}
