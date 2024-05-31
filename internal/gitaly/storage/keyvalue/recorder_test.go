package keyvalue

import (
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestRecordingReadWriter(t *testing.T) {
	db, err := NewBadgerStore(testhelper.SharedLogger(t), t.TempDir())
	require.NoError(t, err)
	defer testhelper.MustClose(t, db)

	preTX := db.NewTransaction(true)

	// Create some existing keys in the database. We'll read one of them later.
	require.NoError(t, preTX.Set([]byte("prefix-1/key-1"), []byte("prefix-1/value-1")))
	require.NoError(t, preTX.Commit())

	tx := db.NewTransaction(true)
	defer tx.Discard()

	rw := NewRecordingReadWriter(tx)

	// Set and delete some keys. These should become part of the write set.
	require.NoError(t, rw.Set([]byte("key-1"), []byte("value-1")))
	require.NoError(t, rw.Set([]byte("key-2"), []byte("value-2")))

	require.NoError(t, rw.Set([]byte("prefix-2/key-1"), []byte("prefix-2/value-1")))
	require.NoError(t, rw.Set([]byte("prefix-2/key-2"), []byte("prefix-2/value-2")))

	// The keys read directly should become part of the read set.
	_, err = rw.Get([]byte("prefix-1/key-1"))
	require.NoError(t, err)

	_, err = rw.Get([]byte("key-not-found"))
	require.Equal(t, badger.ErrKeyNotFound, err)

	// Iterated keys should become part of the read set and the prefix should also
	// be recorded.
	unprefixedIterator := rw.NewIterator(IteratorOptions{})
	defer unprefixedIterator.Close()

	RequireIterator(t, unprefixedIterator, KeyValueState{
		"key-1":          "value-1",
		"key-2":          "value-2",
		"prefix-1/key-1": "prefix-1/value-1",
		"prefix-2/key-1": "prefix-2/value-1",
		"prefix-2/key-2": "prefix-2/value-2",
	})

	// Iterated keys should become part of the read set and the prefix should also
	// be recorded.
	prefixedIterator := rw.NewIterator(IteratorOptions{Prefix: []byte("prefix-2/")})
	defer prefixedIterator.Close()

	RequireIterator(t, prefixedIterator, KeyValueState{
		"prefix-2/key-1": "prefix-2/value-1",
		"prefix-2/key-2": "prefix-2/value-2",
	})

	require.NoError(t, rw.Delete([]byte("deleted-key")))
	require.NoError(t, rw.Delete([]byte("unread-key")))

	require.Equal(t, rw.ReadSet(), KeySet{
		"key-1":          {},
		"key-2":          {},
		"key-not-found":  {},
		"prefix-1/key-1": {},
		"prefix-2/key-1": {},
		"prefix-2/key-2": {},
	})

	require.Equal(t, rw.WriteSet(), KeySet{
		"key-1":          {},
		"key-2":          {},
		"unread-key":     {},
		"deleted-key":    {},
		"prefix-2/key-1": {},
		"prefix-2/key-2": {},
	})

	require.Equal(t, rw.PrefixesRead(), KeySet{
		"":          {},
		"prefix-2/": {},
	})
}
