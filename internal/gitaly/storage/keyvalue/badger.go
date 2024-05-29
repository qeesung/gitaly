package keyvalue

import (
	"fmt"

	"github.com/dgraph-io/badger/v4"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

// NewBadgerStore returns a new Store backed by a Badger database at the given path.
func NewBadgerStore(logger log.Logger, databasePath string) (Store, error) {
	dbOptions := badger.DefaultOptions(databasePath)
	// Enable SyncWrites to ensure all writes are persisted to disk before considering
	// them committed.
	dbOptions.SyncWrites = true
	dbOptions.Logger = badgerLogger{logger}

	db, err := badger.Open(dbOptions)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	return badgerStore{db: db}, nil
}

type badgerLogger struct {
	log.Logger
}

func (l badgerLogger) Debugf(msg string, args ...any) {
	l.Debug(fmt.Sprintf(msg, args...))
}

func (l badgerLogger) Infof(msg string, args ...any) {
	l.Info(fmt.Sprintf(msg, args...))
}

func (l badgerLogger) Warningf(msg string, args ...any) {
	l.Warn(fmt.Sprintf(msg, args...))
}

func (l badgerLogger) Errorf(msg string, args ...any) {
	l.Error(fmt.Sprintf(msg, args...))
}

type badgerStore struct {
	db *badger.DB
}

func (s badgerStore) GetSequence(key []byte, bandwidth uint64) (*badger.Sequence, error) {
	return s.db.GetSequence(key, bandwidth)
}

func (s badgerStore) RunValueLogGC(discardRatio float64) error {
	return s.db.RunValueLogGC(discardRatio)
}

func (s badgerStore) Close() error {
	return s.db.Close()
}

func (s badgerStore) NewTransaction(readWrite bool) Transaction {
	return newBadgerTransaction(s.db.NewTransaction(readWrite))
}

func (s badgerStore) View(handle func(txn ReadWriter) error) error {
	return s.db.View(func(txn *badger.Txn) error {
		return handle(newBadgerTransaction(txn))
	})
}

func (s badgerStore) Update(handle func(txn ReadWriter) error) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return handle(newBadgerTransaction(txn))
	})
}

func (s badgerStore) NewWriteBatch() WriteBatch {
	return s.db.NewWriteBatch()
}

type badgerIterator struct {
	*badger.Iterator
}

func (it badgerIterator) Item() Item {
	return it.Iterator.Item()
}

type badgerTransaction struct {
	txn *badger.Txn
}

func newBadgerTransaction(txn *badger.Txn) Transaction {
	return badgerTransaction{txn: txn}
}

func (txn badgerTransaction) NewIterator(opts IteratorOptions) Iterator {
	return badgerIterator{
		Iterator: txn.txn.NewIterator(badger.IteratorOptions{Prefix: opts.Prefix}),
	}
}

func (txn badgerTransaction) Get(key []byte) (Item, error) {
	return txn.txn.Get(key)
}

func (txn badgerTransaction) Set(key, value []byte) error {
	return txn.txn.Set(key, value)
}

func (txn badgerTransaction) Delete(key []byte) error {
	return txn.txn.Delete(key)
}

func (txn badgerTransaction) Commit() error {
	return txn.txn.Commit()
}

func (txn badgerTransaction) Discard() {
	txn.txn.Discard()
}
