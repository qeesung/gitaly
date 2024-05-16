package storage

import (
	"encoding/binary"
	"strconv"
)

// PartitionID uniquely identifies a partition.
type PartitionID uint64

// MarshalBinary returns a binary representation of the PartitionID.
func (id PartitionID) MarshalBinary() []byte {
	marshaled := make([]byte, binary.Size(id))
	binary.BigEndian.PutUint64(marshaled, uint64(id))
	return marshaled
}

// UnmarshalBinary parses a binary representation of the PartitionID.
func (id *PartitionID) UnmarshalBinary(data []byte) {
	*id = PartitionID(binary.BigEndian.Uint64(data))
}

// String returns a base 10 string representation of the PartitionID.
func (id PartitionID) String() string {
	return strconv.FormatUint(uint64(id), 10)
}
