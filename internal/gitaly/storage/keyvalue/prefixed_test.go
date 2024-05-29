package keyvalue

import (
	"fmt"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestPrefixedTransactioner(t *testing.T) {
	unprefixedDB, err := NewBadgerStore(testhelper.SharedLogger(t), t.TempDir())
	require.NoError(t, err)
	defer testhelper.MustClose(t, unprefixedDB)

	prefixDB1 := NewPrefixedTransactioner(unprefixedDB, []byte("prefix-1/"))
	prefixDB2 := NewPrefixedTransactioner(unprefixedDB, []byte("prefix-2/"))

	nextValue := 0
	generateValue := func() []byte {
		nextValue++
		return []byte(fmt.Sprintf("value-%d", nextValue))
	}

	for _, db := range []Transactioner{unprefixedDB, prefixDB1, prefixDB2} {
		tx := db.NewTransaction(true)
		require.NoError(t, tx.Set([]byte("key-1"), generateValue()), err)
		require.NoError(t, tx.Commit())

		require.NoError(t, db.Update(func(tx ReadWriter) error {
			return tx.Set([]byte("key-2"), generateValue())
		}))

		wb := db.NewWriteBatch()
		require.NoError(t, wb.Set([]byte("key-3"), generateValue()))
		require.NoError(t, wb.Flush())
	}

	for _, methodTC := range []struct {
		desc             string
		runInTransaction func(t *testing.T, db Transactioner, runTest func(ReadWriter))
	}{
		{
			desc: "NewTransaction",
			runInTransaction: func(t *testing.T, db Transactioner, runTest func(ReadWriter)) {
				tx := db.NewTransaction(false)
				defer tx.Discard()

				runTest(tx)
			},
		},
		{
			desc: "View",
			runInTransaction: func(t *testing.T, db Transactioner, runTest func(ReadWriter)) {
				require.NoError(t, db.View(func(tx ReadWriter) error {
					runTest(tx)
					return nil
				}))
			},
		},
		{
			desc: "Update",
			runInTransaction: func(t *testing.T, db Transactioner, runTest func(ReadWriter)) {
				require.NoError(t, db.Update(func(tx ReadWriter) error {
					runTest(tx)
					return nil
				}))
			},
		},
	} {
		t.Run(methodTC.desc, func(t *testing.T) {
			t.Run("NewIterator", func(t *testing.T) {
				for _, tc := range []struct {
					desc          string
					db            Transactioner
					expectedState KeyValueState
				}{
					{
						desc: "unprefixed",
						db:   unprefixedDB,
						expectedState: KeyValueState{
							"key-1":          "value-1",
							"key-2":          "value-2",
							"key-3":          "value-3",
							"prefix-1/key-1": "value-4",
							"prefix-1/key-2": "value-5",
							"prefix-1/key-3": "value-6",
							"prefix-2/key-1": "value-7",
							"prefix-2/key-2": "value-8",
							"prefix-2/key-3": "value-9",
						},
					},
					{
						desc: "prefix-1",
						db:   prefixDB1,
						expectedState: map[string]string{
							"key-1": "value-4",
							"key-2": "value-5",
							"key-3": "value-6",
						},
					},
					{
						desc: "prefix-2",
						db:   prefixDB2,
						expectedState: map[string]string{
							"key-1": "value-7",
							"key-2": "value-8",
							"key-3": "value-9",
						},
					},
				} {
					t.Run(tc.desc, func(t *testing.T) {
						methodTC.runInTransaction(t, tc.db, func(tx ReadWriter) {
							iterator := tx.NewIterator(IteratorOptions{})
							defer iterator.Close()

							RequireIterator(t, iterator, tc.expectedState)
						})
					})
				}
			})

			t.Run("Get", func(t *testing.T) {
				for _, tc := range []struct {
					desc          string
					db            Transactioner
					expectedState map[string]any
				}{
					{
						desc: "unprefixed",
						db:   unprefixedDB,
						expectedState: map[string]any{
							"key-1":          "value-1",
							"prefix-1/key-2": "value-5",
						},
					},
					{
						desc: "prefix-1",
						db:   prefixDB1,
						expectedState: map[string]any{
							"key-1":          "value-4",
							"prefix-1/key-2": badger.ErrKeyNotFound,
						},
					},
				} {
					t.Run(tc.desc, func(t *testing.T) {
						methodTC.runInTransaction(t, tc.db, func(tx ReadWriter) {
							item1, err := tx.Get([]byte("key-1"))
							require.NoError(t, err)
							value1, err := item1.ValueCopy(nil)
							require.NoError(t, err)

							key2 := "prefix-1/key-2"
							var value2 any
							if item, err := tx.Get([]byte(key2)); err != nil {
								value2 = err
							} else {
								key2 = string(item.Key())

								value, err := item.ValueCopy(nil)
								require.NoError(t, err)
								value2 = string(value)
							}

							require.Equal(t, tc.expectedState, map[string]any{
								string(item1.Key()): string(value1),
								key2:                value2,
							})
						})
					})
				}
			})
		})
	}

	t.Run("Delete", func(*testing.T) {
		tx := prefixDB1.NewTransaction(true)
		require.NoError(t, tx.Delete([]byte("key-1")), err)
		require.NoError(t, tx.Commit())

		require.NoError(t, prefixDB1.Update(func(tx ReadWriter) error {
			return tx.Delete([]byte("key-3"))
		}))

		writeBatch := prefixDB2.NewWriteBatch()
		require.NoError(t, writeBatch.Delete([]byte("key-2")))
		require.NoError(t, writeBatch.Flush())

		require.NoError(t, prefixDB1.View(func(tx ReadWriter) error {
			iterator := tx.NewIterator(IteratorOptions{})
			defer iterator.Close()

			RequireIterator(t, iterator, KeyValueState{
				"key-2": "value-5",
			})

			return nil
		}))

		require.NoError(t, prefixDB2.View(func(tx ReadWriter) error {
			iterator := tx.NewIterator(IteratorOptions{})
			defer iterator.Close()

			RequireIterator(t, iterator, KeyValueState{
				"key-1": "value-7",
				"key-3": "value-9",
			})

			return nil
		}))

		require.NoError(t, unprefixedDB.View(func(tx ReadWriter) error {
			iterator := tx.NewIterator(IteratorOptions{})
			defer iterator.Close()

			RequireIterator(t, iterator, KeyValueState{
				"key-1":          "value-1",
				"key-2":          "value-2",
				"key-3":          "value-3",
				"prefix-1/key-2": "value-5",
				"prefix-2/key-1": "value-7",
				"prefix-2/key-3": "value-9",
			})

			return nil
		}))
	})
}

// KeyValueState describes the expected state of the key-value store. The keys in the map are the expected keys
// in the store and the values are the expected values.
type KeyValueState map[string]string

// RequireIterator asserts the iterator returns the expected key-value state.
func RequireIterator(tb testing.TB, iterator Iterator, expectedState KeyValueState) {
	tb.Helper()

	actualState := KeyValueState{}

	for iterator.Rewind(); iterator.Valid(); iterator.Next() {
		key := iterator.Item().Key()

		require.NoError(tb, iterator.Item().Value(func(value []byte) error {
			actualState[string(key)] = string(value)
			return nil
		}))
	}

	require.Equal(tb, expectedState, actualState)
}
