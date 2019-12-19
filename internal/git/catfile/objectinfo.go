package catfile

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
)

// ObjectInfo represents a header returned by `git cat-file --batch`
type ObjectInfo struct {
	Oid  string
	Type string
	Size int64
}

// NotFoundError is returned when requesting an object that does not exist.
type NotFoundError struct{ error }

// IsNotFound tests whether err has type NotFoundError.
func IsNotFound(err error) bool {
	_, ok := err.(NotFoundError)
	return ok
}

// IsBlob returns true if object type is "blob"
func (o *ObjectInfo) IsBlob() bool {
	return o.Type == "blob"
}

// ParseObjectInfo reads from a reader and parses the data into an ObjectInfo struct
func ParseObjectInfo(stdout *bufio.Reader) (*ObjectInfo, error) {
	infoLine, err := stdout.ReadSlice('\n')
	if err != nil {
		return nil, fmt.Errorf("read info line: %v", err)
	}

	infoLine = bytes.TrimSuffix(infoLine, []byte{'\n'})
	if bytes.HasSuffix(infoLine, []byte(" missing")) {
		return nil, NotFoundError{fmt.Errorf("object not found")}
	}

	info := bytes.Split(infoLine, []byte{' '})
	if len(info) != 3 {
		return nil, fmt.Errorf("invalid info line: %q", infoLine)
	}

	objectSize, err := strconv.ParseInt(string(info[2]), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse object size: %v", err)
	}

	return &ObjectInfo{
		Oid:  string(info[0]),
		Type: string(info[1]),
		Size: objectSize,
	}, nil
}
