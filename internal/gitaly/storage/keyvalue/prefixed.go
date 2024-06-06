package keyvalue

type prefixedTransactioner struct {
	transactioner Transactioner
	prefix        []byte
}

// NewPrefixedTransactioner wraps the transactioner and applies the given prefix to every
// key in operations performed through it.
func NewPrefixedTransactioner(transactioner Transactioner, prefix []byte) Transactioner {
	return prefixedTransactioner{transactioner: transactioner, prefix: prefix}
}

func (p prefixedTransactioner) NewTransaction(readWrite bool) Transaction {
	txn := p.transactioner.NewTransaction(readWrite)

	return prefixedTransaction{
		ReadWriter:  NewPrefixedReadWriter(txn, p.prefix),
		transaction: txn,
		prefix:      p.prefix,
	}
}

func (p prefixedTransactioner) NewWriteBatch() WriteBatch {
	return prefixedWriteBatch{
		writeBatch: p.transactioner.NewWriteBatch(),
		prefix:     p.prefix,
	}
}

func (p prefixedTransactioner) Update(handle func(ReadWriter) error) error {
	return p.transactioner.Update(func(txn ReadWriter) error {
		return handle(NewPrefixedReadWriter(txn, p.prefix))
	})
}

func (p prefixedTransactioner) View(handle func(ReadWriter) error) error {
	return p.transactioner.View(func(txn ReadWriter) error {
		return handle(NewPrefixedReadWriter(txn, p.prefix))
	})
}

type prefixedTransaction struct {
	ReadWriter
	transaction Transaction
	prefix      []byte
}

func (p prefixedTransaction) Commit() error {
	return p.transaction.Commit()
}

func (p prefixedTransaction) Discard() {
	p.transaction.Discard()
}

type prefixedWriteBatch struct {
	writeBatch WriteBatch
	prefix     []byte
}

func (p prefixedWriteBatch) Set(key, value []byte) error {
	return p.writeBatch.Set(addPrefix(p.prefix, key), value)
}

func (p prefixedWriteBatch) Delete(key []byte) error {
	return p.writeBatch.Delete(addPrefix(p.prefix, key))
}

func (p prefixedWriteBatch) Flush() error {
	return p.writeBatch.Flush()
}

func (p prefixedWriteBatch) Cancel() {
	p.writeBatch.Cancel()
}

type prefixedReadWriter struct {
	readWriter ReadWriter
	prefix     []byte
}

// NewPrefixedReadWriter returns ReadWriter that wraps the given read writer
// and prefixes every key with the given prefix.
func NewPrefixedReadWriter(readWriter ReadWriter, prefix []byte) ReadWriter {
	return prefixedReadWriter{
		readWriter: readWriter,
		prefix:     prefix,
	}
}

func (p prefixedReadWriter) NewIterator(opts IteratorOptions) Iterator {
	opts.Prefix = addPrefix(p.prefix, opts.Prefix)
	return prefixedIterator{
		iterator: p.readWriter.NewIterator(opts),
		prefix:   p.prefix,
	}
}

func (p prefixedReadWriter) Get(key []byte) (Item, error) {
	item, err := p.readWriter.Get(addPrefix(p.prefix, key))
	if err != nil {
		return nil, err
	}

	return newPrefixedItem(item, p.prefix), nil
}

func (p prefixedReadWriter) Set(key, value []byte) error {
	return p.readWriter.Set(addPrefix(p.prefix, key), value)
}

func (p prefixedReadWriter) Delete(key []byte) error {
	return p.readWriter.Delete(addPrefix(p.prefix, key))
}

type prefixedIterator struct {
	iterator Iterator
	prefix   []byte
}

func (p prefixedIterator) Rewind() {
	p.iterator.Rewind()
}

func (p prefixedIterator) Next() {
	p.iterator.Next()
}

func (p prefixedIterator) Item() Item {
	return newPrefixedItem(p.iterator.Item(), p.prefix)
}

func (p prefixedIterator) Valid() bool {
	return p.iterator.Valid()
}

func (p prefixedIterator) Close() {
	p.iterator.Close()
}

type prefixedItem struct {
	item   Item
	prefix []byte
}

func newPrefixedItem(item Item, prefix []byte) prefixedItem {
	return prefixedItem{
		item:   item,
		prefix: prefix,
	}
}

func (p prefixedItem) Key() []byte {
	return p.item.Key()[len(p.prefix):]
}

func (p prefixedItem) Value(handle func([]byte) error) error {
	return p.item.Value(handle)
}

func (p prefixedItem) ValueCopy(dst []byte) ([]byte, error) {
	return p.item.ValueCopy(dst)
}

func addPrefix(prefix, key []byte) []byte {
	prefixedKey := make([]byte, 0, len(prefix)+len(key))
	return append(append(prefixedKey, prefix...), key...)
}
