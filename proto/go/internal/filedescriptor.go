package internal

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io/ioutil"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
)

// ExtractFile extracts a FileDescriptorProto from a gzip'd buffer.
// Note: function is copied from the github.com/golang/protobuf dependency:
// https://github.com/golang/protobuf/blob/9eb2c01ac278a5d89ce4b2be68fe4500955d8179/descriptor/descriptor.go#L50
func ExtractFile(gz []byte) (*descriptor.FileDescriptorProto, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip reader: %v", err)
	}
	defer r.Close()

	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to uncompress descriptor: %v", err)
	}

	fd := new(descriptor.FileDescriptorProto)
	if err := proto.Unmarshal(b, fd); err != nil {
		return nil, fmt.Errorf("malformed FileDescriptorProto: %v", err)
	}

	return fd, nil
}
