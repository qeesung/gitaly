package protoregistry

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"

	"github.com/golang/protobuf/proto"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
)

const protobufTag = "protobuf"

// reflectFindRepoTarget finds the target repository by using the OID to
// navigate the struct tags
// Warning: this reflection filled function is full of forbidden dark elf magic
func reflectFindRepoTarget(pbMsg proto.Message, targetOID []int) (*gitalypb.Repository, error) {
	var targetRepo *gitalypb.Repository

	msgV := reflect.ValueOf(pbMsg)

	for _, fieldNo := range targetOID {
		var err error

		msgV, err = findProtoField(msgV, fieldNo)
		if err != nil {
			return nil, err
		}
	}

	targetRepo, ok := msgV.Interface().(*gitalypb.Repository)
	if !ok {
		return nil, fmt.Errorf("repo target OID %v points to non-Repo type %+v", targetOID, msgV.Interface())
	}

	return targetRepo, nil
}

// matches a tag string like "bytes,1,opt,name=repository,proto3"
var protobufTagRegex = regexp.MustCompile(`^(.*?),(\d+),(.*?),name=(.*?),proto3$`)

func findProtoField(msgV reflect.Value, protoField int) (reflect.Value, error) {
	msgV = reflect.Indirect(msgV)
	for i := 0; i < msgV.NumField(); i++ {
		field := msgV.Type().Field(i)
		tag := field.Tag.Get(protobufTag)

		matches := protobufTagRegex.FindStringSubmatch(tag)
		if len(matches) != 5 {
			continue
		}

		fieldStr := matches[2]
		if fieldStr == strconv.Itoa(protoField) {
			return msgV.FieldByName(field.Name), nil
		}
	}

	err := fmt.Errorf(
		"unable to find protobuf field %d in message %s",
		protoField, msgV.Type().Name(),
	)
	return reflect.Value{}, err
}
