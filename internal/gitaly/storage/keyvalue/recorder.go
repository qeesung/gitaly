package keyvalue

// KeySet contains a set of keys stored as the keys of the map.
type KeySet map[string]struct{}

func (s KeySet) record(key []byte) {
	s[string(key)] = struct{}{}
}

// RecordingReadWriter wraps a ReadWriter and records its read
// and write sets.
type RecordingReadWriter struct {
	rw ReadWriter

	prefixesRead KeySet
	readSet      KeySet
	writeSet     KeySet
}

// NewRecordingReadWriter wraps the passed in ReadWriter and records
// the write set of the read writer.
func NewRecordingReadWriter(rw ReadWriter) RecordingReadWriter {
	return RecordingReadWriter{
		rw:           rw,
		prefixesRead: KeySet{},
		readSet:      KeySet{},
		writeSet:     KeySet{},
	}
}

// WriteSet returns the write set of the ReadWriter. The keys in the map
// are the keys that have been set or deleted.
func (r RecordingReadWriter) WriteSet() KeySet {
	return r.writeSet
}

// ReadSet returns the read set of the ReadWriter. The keys in the map
// are the keys that have been read.
func (r RecordingReadWriter) ReadSet() KeySet {
	return r.readSet
}

// PrefixesRead returns the key prefixes that were iterated.
func (r RecordingReadWriter) PrefixesRead() KeySet {
	return r.prefixesRead
}

// NewIterator returns a new iterator with the given options. The prefix
// of the iterator is recorded in the prefixes read. Refer to the Store
// interface for more documentation.
func (r RecordingReadWriter) NewIterator(opts IteratorOptions) Iterator {
	r.prefixesRead.record(opts.Prefix)
	return recordingIterator{
		iterator: r.rw.NewIterator(opts),
		readSet:  r.readSet,
	}
}

// Get gets a key and records it in the read set. Refer to the
// Store interface for more documentation.
func (r RecordingReadWriter) Get(key []byte) (Item, error) {
	r.readSet.record(key)
	return r.rw.Get(key)
}

// Set sets a key and records it in the write set. Refer to the
// Store interface for more documentation.
func (r RecordingReadWriter) Set(key, value []byte) error {
	r.writeSet.record(key)
	return r.rw.Set(key, value)
}

// Delete deletes a key and records it in the write set. Refer
// to the Store interface for more documentation.
func (r RecordingReadWriter) Delete(key []byte) error {
	r.writeSet.record(key)
	return r.rw.Delete(key)
}

type recordingIterator struct {
	iterator Iterator
	readSet  KeySet
}

func (r recordingIterator) Rewind() {
	r.iterator.Rewind()
}

func (r recordingIterator) Next() {
	r.iterator.Next()
}

func (r recordingIterator) Item() Item {
	item := r.iterator.Item()

	r.readSet.record(item.Key())

	return item
}

func (r recordingIterator) Valid() bool {
	return r.iterator.Valid()
}

func (r recordingIterator) Close() {
	r.iterator.Close()
}
