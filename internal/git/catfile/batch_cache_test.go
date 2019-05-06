package catfile

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCacheAdd(t *testing.T) {
	const maxLen = 3
	bc := newCache(time.Hour, maxLen)

	key0 := testKey(0)
	value0 := testValue()
	bc.Add(key0, value0)
	requireCacheValid(t, bc)

	key1 := testKey(1)
	bc.Add(key1, testValue())
	requireCacheValid(t, bc)

	key2 := testKey(2)
	bc.Add(key2, testValue())
	requireCacheValid(t, bc)

	// Because maxLen is 3, and key0 is oldest, we expect that adding key3
	// will kick out key0.
	key3 := testKey(3)
	bc.Add(key3, testValue())
	requireCacheValid(t, bc)

	require.Equal(t, maxLen, bc.len(), "length should be maxLen")
	require.True(t, value0.isClosed(), "value0 should be closed")
	require.Equal(t, []key{key1, key2, key3}, keys(bc))
}

func TestCacheAddTwice(t *testing.T) {
	bc := newCache(time.Hour, 10)

	key0 := testKey(0)
	value0 := testValue()
	bc.Add(key0, value0)
	requireCacheValid(t, bc)

	key1 := testKey(1)
	bc.Add(key1, testValue())
	requireCacheValid(t, bc)

	require.Equal(t, key0, bc.head().key, "key0 should be oldest key")

	value2 := testValue()
	bc.Add(key0, value2)
	requireCacheValid(t, bc)

	require.Equal(t, key1, bc.head().key, "key1 should be oldest key")
	require.Equal(t, value2, bc.head().value)

	require.True(t, value0.isClosed(), "value0 should be closed")
}

func TestCacheCheckout(t *testing.T) {
	bc := newCache(time.Hour, 10)

	key0 := testKey(0)
	value0 := testValue()
	bc.Add(key0, value0)

	v, ok := bc.Checkout(key{sessionID: "foo"})
	requireCacheValid(t, bc)
	require.Nil(t, v, "expect nil value when key not found")
	require.False(t, ok, "ok flag")

	v, ok = bc.Checkout(key0)
	requireCacheValid(t, bc)

	require.Equal(t, value0, v)
	require.True(t, ok, "ok flag")

	require.False(t, v.isClosed(), "value should not be closed after checkout")

	v, ok = bc.Checkout(key0)
	require.False(t, ok, "ok flag after second checkout")
	require.Nil(t, v, "value from second checkout")
}

func TestCacheEnforceTTL(t *testing.T) {
	ttl := time.Hour
	bc := newCache(ttl, 10)

	sleep := func() { time.Sleep(2 * time.Millisecond) }

	key0 := testKey(0)
	value0 := testValue()
	bc.Add(key0, value0)
	sleep()

	key1 := testKey(1)
	value1 := testValue()
	bc.Add(key1, value1)
	sleep()

	cutoff := time.Now().Add(ttl)
	sleep()

	key2 := testKey(2)
	bc.Add(key2, testValue())
	sleep()

	key3 := testKey(3)
	bc.Add(key3, testValue())
	sleep()

	requireCacheValid(t, bc)

	// We expect this cutoff to cause eviction of key0 and key1 but no other keys.
	bc.EnforceTTL(cutoff)

	requireCacheValid(t, bc)

	for i, v := range []*Batch{value0, value1} {
		require.True(t, v.isClosed(), "value %d %v should be closed", i, v)
	}

	require.Equal(t, []key{key2, key3}, keys(bc), "remaining keys after EnforceTTL")

	bc.EnforceTTL(cutoff)

	requireCacheValid(t, bc)
	require.Equal(t, []key{key2, key3}, keys(bc), "remaining keys after second EnforceTTL")
}

func TestAutoExpiry(t *testing.T) {
	ttl := 5 * time.Millisecond
	bc := newCacheRefresh(ttl, 10, 1*time.Millisecond)

	key0 := testKey(0)
	value0 := testValue()
	bc.Add(key0, value0)
	requireCacheValid(t, bc)

	bc.Lock()
	require.Contains(t, bc.keyMap, key0, "key should still be in map")
	require.False(t, value0.isClosed(), "value should not have been closed")
	bc.Unlock()

	time.Sleep(2 * ttl)

	bc.Lock()
	require.NotContains(t, bc.keyMap, key0, "key should no longer be in map")
	require.True(t, value0.isClosed(), "value should be closed after eviction")
	bc.Unlock()
}

func requireCacheValid(t *testing.T, bc *batchCache) {
	bc.Lock()
	defer bc.Unlock()

	lenMap, lenList := len(bc.keyMap), bc.ll.Len()
	require.Equal(t, lenMap, lenList, "keyMap %d entries %d", lenMap, lenList)

	for e := bc.ll.Front(); e != nil; e = e.Next() {
		ent := e.Value.(*entry)
		require.Equal(t, e, bc.keyMap[ent.key], "reverse key index")

		v := ent.value
		require.False(t, v.isClosed(), "values in cache should not be closed: %v %v", ent, v)
	}
}

func testValue() *Batch { return &Batch{} }

func testKey(i int) key { return key{sessionID: fmt.Sprintf("key-%d", i)} }

func keys(bc *batchCache) []key {
	var result []key
	for e := bc.ll.Front(); e != nil; e = e.Next() {
		ent := e.Value.(*entry)
		result = append(result, ent.key)
	}

	return result
}

func BenchmarkCacheHash10(b *testing.B) {
	benchMap(b, 10)
}

func BenchmarkCacheHash100(b *testing.B) {
	benchMap(b, 100)
}

func BenchmarkCacheHash1000(b *testing.B) {
	benchMap(b, 1000)
}

func BenchmarkCacheHash10000(b *testing.B) {
	benchMap(b, 10000)
}

func benchMap(b *testing.B, n int) {
	bc := newCache(time.Hour, n)
	benchCacheAdd(b, bc, n)
}

func BenchmarkCacheList10(b *testing.B) {
	benchList(b, 10)
}

func BenchmarkCacheList100(b *testing.B) {
	benchList(b, 100)
}

func BenchmarkCacheList1000(b *testing.B) {
	benchList(b, 1000)
}

func BenchmarkCacheList10000(b *testing.B) {
	benchList(b, 10000)
}

func benchList(b *testing.B, n int) {
	a := &alt{newCache(time.Hour, n)}
	benchCacheAdd(b, a, n)
}

func BenchmarkCacheSlice10(b *testing.B) {
	benchSlice(b, 10)
}

func BenchmarkCacheSlice100(b *testing.B) {
	benchSlice(b, 100)
}

func BenchmarkCacheSlice1000(b *testing.B) {
	benchSlice(b, 1000)
}

func BenchmarkCacheSlice10000(b *testing.B) {
	benchSlice(b, 10000)
}

func benchSlice(b *testing.B, n int) {
	a := &slice{batchCache: newCache(time.Hour, n)}
	benchCacheAdd(b, a, n)
}

type testInterface interface {
	Add(key, *Batch)
	Checkout(key) (*Batch, bool)
}

func benchCacheAdd(b *testing.B, testCache testInterface, n int) {
	stuff(testCache, n)
	b.ResetTimer()

	for j := 0; j < b.N; j++ {
		k := testKey(j + n)
		testCache.Add(k, testValue())
		if _, ok := testCache.Checkout(k); !ok {
			b.Fatal("checkout failed")
		}
	}
}

func stuff(bc testInterface, n int) {
	for i := 0; i < n; i++ {
		bc.Add(testKey(i), testValue())
	}
}
