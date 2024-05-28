package keyvalue

import "github.com/dgraph-io/badger/v4"

// Item is the interface of badger.Item. Refer to Badger's documentation for details.
type Item interface {
	Key() []byte
	Value(func(value []byte) error) error
	ValueCopy([]byte) ([]byte, error)
}

// Iterator is the interface of badger.Iterator. Refer to Badger's documentation for details.
type Iterator interface {
	Rewind()
	Next()
	Item() Item
	Valid() bool
	Close()
}

// IteratorOptions are subset of badger.IteratorOptions. Refer to Badger's documentation for details.
type IteratorOptions struct {
	Prefix []byte
}

// ReadWriter is a subset of Transaction's interface that only allows for reading and writing data.
type ReadWriter interface {
	NewIterator(opts IteratorOptions) Iterator
	Get(key []byte) (Item, error)
	Set(key, value []byte) error
	Delete(key []byte) error
}

// Transaction is the interface of badger.Txn. Refer to Badger's documentation for details.
type Transaction interface {
	ReadWriter
	Commit() error
	Discard()
}

// WriteBatch is the interface of badger.WriteBatch. Refer to Badger's documentation for details.
type WriteBatch interface {
	Set(key, value []byte) error
	Delete(key []byte) error
	Flush() error
	Cancel()
}

// Transactioner is a subset of Store's interface that only allows for starting transactions.
type Transactioner interface {
	NewTransaction(readWrite bool) Transaction
	View(func(tx ReadWriter) error) error
	Update(func(tx ReadWriter) error) error
	NewWriteBatch() WriteBatch
}

// Store is the interface of badger.DB. Refer to Badger's documentation for details.
type Store interface {
	Transactioner
	GetSequence([]byte, uint64) (*badger.Sequence, error)
	RunValueLogGC(float64) error
	Close() error
}
