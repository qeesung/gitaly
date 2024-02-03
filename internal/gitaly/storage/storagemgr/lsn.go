package storagemgr

import (
	"fmt"
	"math"
	"strconv"

	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// lsnFormatBase is the base used when formatting an LSN as a string.
const lsnFormatBase = 10

// lsnFormat is used as formatting string when printing out LSN values. LSNs are formatted in a fully
// padded form to keep their string representation lexicograpically ordered.
var lsnFormat = "%0" + strconv.FormatUint(uint64(len(strconv.FormatUint(math.MaxUint64, lsnFormatBase))), 10) + "s"

// LSN is a log sequence number that points to a specific position in the partition's write-ahead log.
type LSN uint64

// toProto returns the protobuf representation of LSN for serialization purposes.
func (lsn LSN) toProto() *gitalypb.LSN {
	return &gitalypb.LSN{Value: uint64(lsn)}
}

// String returns a string representation of the LSN.
func (lsn LSN) String() string {
	return fmt.Sprintf(lsnFormat, strconv.FormatUint(uint64(lsn), lsnFormatBase))
}

// parseLSN parses a string representation of an LSN.
func parseLSN(lsn string) (LSN, error) {
	parsedValue, err := strconv.ParseUint(lsn, lsnFormatBase, 64)
	if err != nil {
		return 0, fmt.Errorf("parse uint: %w", err)
	}

	return LSN(parsedValue), nil
}
