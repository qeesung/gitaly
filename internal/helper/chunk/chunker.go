package chunk

// Item could be e.g. a commit in an RPC that returns a chunked stream of
// commits.
type Item interface{}

// Sender encapsulates a gRPC response stream and the current chunk
// that's being built.
//
// Reset, Append, [Append...], Send, Reset, Append, [Append...], Send, ...
type Sender interface {
	// Reset should create a fresh response message.
	Reset()
	// Append should append the given item to the slice in the current response message
	Append(Item)
	// Send should send the current response message
	Send() error
}

// New returns a new Chunker.
func New(s Sender) *Chunker { return &Chunker{s: s} }

// Chunker lets you spread items you want to send over multiple chunks.
// This type is not thread-safe.
type Chunker struct {
	s Sender
	n int
}

// Send will append an item to the current chunk and send the chunk if it is full.
func (c *Chunker) Send(it Item) error {
	if c.n == 0 {
		c.s.Reset()
	}

	c.s.Append(it)
	c.n++

	const chunkSize = 20
	if c.n >= chunkSize {
		return c.sendResponseMsg()
	}

	return nil
}

func (c *Chunker) sendResponseMsg() error {
	c.n = 0
	return c.s.Send()
}

// Flush sends remaining items in the current chunk, if any.
func (c *Chunker) Flush() error {
	if c.n == 0 {
		return nil
	}

	return c.sendResponseMsg()
}
