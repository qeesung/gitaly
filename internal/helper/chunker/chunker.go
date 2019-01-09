package chunker

type Item interface{}

type Sender interface {
	Reset()
	Append(Item)
	Send() error
}

func New(s Sender) *Chunker { return &Chunker{Sender: s} }

type Chunker struct {
	Sender
	n int
}

func (c *Chunker) Send(it Item) error {
	if c.n == 0 {
		c.Sender.Reset()
	}

	c.Sender.Append(it)
	c.n++

	const chunkSize = 20
	if c.n >= chunkSize {
		return c.send()
	}

	return nil
}

func (c *Chunker) send() error {
	c.n = 0
	return c.Sender.Send()
}

func (c *Chunker) Flush() error {
	if c.n == 0 {
		return nil
	}

	return c.send()
}
