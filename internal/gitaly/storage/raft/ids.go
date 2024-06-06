package raft

import (
	"encoding/binary"
	"strconv"
)

// raftID identifies Raft's managed objects, which currently include ReplicaID and
// GroupID. GroupID is analogous to the ShardID type used by the dragonboat library
// internally. In Gitaly, "group" is used exclusively to refer to a Raft group.
type raftID uint64

// MetadataGroupGroupID is a hard-coded ID of the cluster-wide metadata Raft group.
const MetadataGroupGroupID = raftID(1)

// MarshalBinary returns a binary representation of the raftID.
func (id raftID) MarshalBinary() []byte {
	marshaled := make([]byte, binary.Size(id))
	binary.BigEndian.PutUint64(marshaled, uint64(id))
	return marshaled
}

// UnmarshalBinary parses a binary representation of the raftID.
func (id *raftID) UnmarshalBinary(data []byte) {
	*id = raftID(binary.BigEndian.Uint64(data))
}

// String returns a base 10 string representation of the raftID.
func (id raftID) String() string {
	return strconv.FormatUint(uint64(id), 10)
}

func (id raftID) ToUint64() uint64 {
	return uint64(id)
}
