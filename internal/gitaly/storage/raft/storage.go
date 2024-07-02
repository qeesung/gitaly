package raft

import (
	"context"
	"errors"
	"fmt"

	"github.com/dgraph-io/badger/v4"
	"github.com/lni/dragonboat/v4"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
)

type dbAccessor func(context.Context, func(keyvalue.ReadWriter) error) error

// newStorageManager returns an instance of storage manager.
func newStorageManager(name string, ptnMgr *storagemgr.PartitionManager, nodeHost *dragonboat.NodeHost) *storageManager {
	return &storageManager{
		name:     name,
		ptnMgr:   ptnMgr,
		nodeHost: nodeHost,
	}
}

// Close closes the storage manager.
func (m *storageManager) Close() { m.nodeHost.Close() }

func (m *storageManager) loadStorageID() error {
	return m.storageDB.View(func(txn keyvalue.ReadWriter) error {
		item, err := txn.Get([]byte("storage_id"))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return nil
			}
			return err
		}
		return item.Value(func(value []byte) error {
			m.id.UnmarshalBinary(value)
			return nil
		})
	})
}

func (m *storageManager) saveStorageID(id raftID) error {
	return m.storageDB.Update(func(txn keyvalue.ReadWriter) error {
		_, err := txn.Get([]byte("storage_id"))
		if err == nil {
			return fmt.Errorf("storage ID already exists")
		} else if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}
		if err := txn.Set([]byte("storage_id"), id.MarshalBinary()); err != nil {
			return err
		}
		m.id = id
		return nil
	})
}

// clearStorageID clears the storage ID inside the in-memory storage of the storage manager. It does
// not clean the underlying storage ID.
func (m *storageManager) clearStorageID() { m.id = 0 }

func (m *storageManager) dbForStorage() dbAccessor {
	return func(ctx context.Context, fn func(keyvalue.ReadWriter) error) error {
		return m.ptnMgr.StorageKV(ctx, m.name, func(rw keyvalue.ReadWriter) error {
			return fn(keyvalue.NewPrefixedReadWriter(rw, []byte("raft")))
		})
	}
}

func (m *storageManager) dbForGroup(groupID raftID, replicaID raftID) dbAccessor {
	return func(ctx context.Context, fn func(keyvalue.ReadWriter) error) error {
		return m.ptnMgr.StorageKV(ctx, m.name, func(rw keyvalue.ReadWriter) error {
			return fn(keyvalue.NewPrefixedReadWriter(
				rw,
				[]byte(fmt.Sprintf("%s/%s", groupID.MarshalBinary(), replicaID.MarshalBinary())),
			))
		})
	}
}
