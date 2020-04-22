package chunk

import (
	"reflect"

	"github.com/golang/protobuf/proto"
)

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
	s    Sender
	size int
}

// MaxMessageSize is the default gRPC maximum message size, 4MB
const MaxMessageSize = 4 * 1024 * 1024

// Send will append an item to the current chunk and send the chunk if it is full.
func (c *Chunker) Send(it Item) error {
	if c.size == 0 {
		c.s.Reset()
	}

	var itSize int

	switch v := it.(type) {
	case proto.Message:
		itSize = proto.Size(v)
	case []byte:
		itSize = len(v)
	case string:
		itSize = len(v)
	default:
		itSize = int(reflect.TypeOf(it).Size())
	}

	if itSize+c.size >= MaxMessageSize {
		if err := c.sendResponseMsg(); err != nil {
			return err
		}
		c.s.Reset()
	}

	c.s.Append(it)
	c.size += itSize

	return nil
}

func (c *Chunker) sendResponseMsg() error {
	c.size = 0
	return c.s.Send()
}

// Flush sends remaining items in the current chunk, if any.
func (c *Chunker) Flush() error {
	if c.size == 0 {
		return nil
	}

	return c.sendResponseMsg()
}
